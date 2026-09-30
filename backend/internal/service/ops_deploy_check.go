package service

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// 部署一致性看门狗（app 侧）。
//
// 背景：2026-09-30 上线前实测发现老机主线一直跑着 2.0.28，而它的 systemd 单元
// 叫 sub2api-2.0.30-canary（ExecStart 指向 2.0.28，从未改过）——nginx 权重 40/60，
// 意味着 40% 的流量在缺两版修复的二进制上跑了很久。
//
// 这类问题**在应用内检测不到**：进程看不到自己的 systemd 单元名，而它的沙箱
// （ProtectSystem=strict）也读不到单元文件。所以检测放在主机层（一个 systemd
// timer + backend/scripts/sub2api-deploy-check.sh），把结论写成一个状态文件；
// 应用这里只负责把状态文件变成指标，从而复用既有的告警邮件管线——
// 这样主机上不需要再存一份 SMTP 口令。
const (
	// OpsMetricDeployVersionMismatch 实际运行的二进制/单元与期望版本不一致时为 1。
	OpsMetricDeployVersionMismatch = "deploy_version_mismatch"
	// OpsMetricDeployCheckAgeSeconds 距上一次成功检查的秒数。
	// 主机上的 timer 挂了、脚本报错了，这个值会一直涨——用来给看门狗本身告警。
	OpsMetricDeployCheckAgeSeconds = "deploy_check_age_seconds"
)

// 状态文件的位置。
//
// 默认放在 /opt/sub2api/ 下——该目录正是各单元 ReadWritePaths 里允许访问的路径，
// 应用在 ProtectSystem=strict 下也能读。可用环境变量覆盖（脚本侧同名配置）。
//
// 注意：这里不用 cfg.*.DataDir，因为 DATA_DIR 在本项目里是「配置文件搜索路径」
// 而不是数据目录，语义不同，拿它拼路径会得到意外的位置。
const (
	defaultDeployCheckStatusPath = "/opt/sub2api/deploy-check.status"
	deployCheckStatusPathEnv     = "SUB2API_DEPLOY_CHECK_STATUS_FILE"
)

// deployCheckStatusPath 返回状态文件的绝对路径（受环境变量覆盖）。
func deployCheckStatusPath() string {
	if override := strings.TrimSpace(os.Getenv(deployCheckStatusPathEnv)); override != "" {
		return override
	}
	return defaultDeployCheckStatusPath
}

// deployCheckStaleAfter 超过这个时长就认为状态文件不可信。
// 此时「版本不一致」指标返回「不可用」而不是 0——避免看门狗自己死掉了
// 却报告一切正常。这种情况由 deploy_check_age_seconds 负责告警。
const deployCheckStaleAfter = 30 * time.Minute

var errDeployCheckUnavailable = errors.New("deploy check status unavailable")

// DeployCheckStatus 是主机层脚本写出的状态。
type DeployCheckStatus struct {
	// VersionMismatch 为 true 表示实际运行的二进制（或它所属的单元）与期望版本不符。
	VersionMismatch bool
	// CheckedAt 是主机脚本完成检查的时刻。
	CheckedAt time.Time
	// Detail 是脚本给出的人类可读说明（用于告警正文）。
	Detail string
}

// Age 返回距检查时刻的时长。
func (s *DeployCheckStatus) Age(now time.Time) time.Duration {
	if s == nil || s.CheckedAt.IsZero() {
		return 0
	}
	return now.Sub(s.CheckedAt)
}

// Stale 表示这份状态是否已经过旧、不可信。
func (s *DeployCheckStatus) Stale(now time.Time) bool {
	if s == nil || s.CheckedAt.IsZero() {
		return true
	}
	return s.Age(now) > deployCheckStaleAfter
}

// ReadDeployCheckStatus 读取并解析主机层写出的状态文件。
//
// statusPath 为空时使用默认路径（受 SUB2API_DEPLOY_CHECK_STATUS_FILE 覆盖）。
// 文件不存在、格式不对、时间戳缺失都返回 errDeployCheckUnavailable。
func ReadDeployCheckStatus(statusPath string) (*DeployCheckStatus, error) {
	statusPath = strings.TrimSpace(statusPath)
	if statusPath == "" {
		statusPath = deployCheckStatusPath()
	}

	raw, err := os.ReadFile(statusPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errDeployCheckUnavailable, err)
	}

	status := &DeployCheckStatus{}
	var checkedAtUnix int64
	var haveCheckedAt bool
	var haveMismatch bool

	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch key {
		case "version_mismatch":
			switch strings.ToLower(value) {
			case "1", "true", "yes":
				status.VersionMismatch = true
			case "0", "false", "no":
				status.VersionMismatch = false
			default:
				return nil, fmt.Errorf("%w: bad version_mismatch %q", errDeployCheckUnavailable, value)
			}
			haveMismatch = true
		case "checked_at_unix":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil || parsed <= 0 {
				return nil, fmt.Errorf("%w: bad checked_at_unix %q", errDeployCheckUnavailable, value)
			}
			checkedAtUnix = parsed
			haveCheckedAt = true
		case "detail":
			status.Detail = value
		}
	}

	if !haveMismatch || !haveCheckedAt {
		return nil, fmt.Errorf("%w: incomplete status file", errDeployCheckUnavailable)
	}
	status.CheckedAt = time.Unix(checkedAtUnix, 0).UTC()
	return status, nil
}
