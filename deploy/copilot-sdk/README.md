# Copilot SDK 配套服务

公开安装与使用说明：[账号在哪里配置、怎样接入 Codex](../../docs/copilot-sdk-codex.md)。

- `prepare.sh`：准备固定版本 ghcp_proxy 与 SDK 依赖，不启动服务。
- `login.py`：GitHub 设备登录；生成 sidecar/signing keys，Token 仅写受限文件。
- `server.py`：鉴权、租户/线程隔离与 Responses 入口；一个进程仅绑定一个 GitHub 账号。
- `sidecar.env.example`、`copilot-sdk-sidecar.service`：同主机 systemd 部署模板。
- `test_server.py`、`test_adapter_contract.py`：离线测试；后者调用真实适配器代码，上游 SDK 事件使用 mock。

本目录为可选配套服务，需单独安装并完成 GitHub 授权。运行状态、OAuth Token、sidecar Key 和 signing key 均保存于仓库外。

测试示例（在适配器 checkout 的虚拟环境中运行）：

```bash
cd /opt/sub2api-copilot/adapter
.venv/bin/python -m pip install pytest pytest-asyncio pytest-socket
PYTHONPATH="$PWD" .venv/bin/python -m pytest --disable-socket --allow-unix-socket -q \
  /opt/sub2api-public/deploy/copilot-sdk/test_server.py \
  /opt/sub2api-public/deploy/copilot-sdk/test_adapter_contract.py
```

生产服务只需要 `prepare.sh` 安装的运行依赖，无需安装测试包。ghcp_proxy 保持独立 checkout，固定 SHA 与 SDK 版本由服务入口验证。
