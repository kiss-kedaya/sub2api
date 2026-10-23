package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ipnotice"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

const gatewayIPNoticeSessionKey = "gateway_ip_notice_session"

// GatewayIPNoticeAllower reuses the shared Redis rate limiter. Its key includes
// only the canonical source IP, so changing credentials cannot reset its quota.
type GatewayIPNoticeAllower interface {
	Allow(context.Context, string, int, time.Duration) (middleware.AllowResult, error)
}

// GatewayIPNoticeSession is a request/connection snapshot for a selected IP.
type GatewayIPNoticeSession struct {
	IP      string
	Message string
	RPM     int
	Limiter GatewayIPNoticeAllower
}

// Allow admits one request/turn. Infrastructure failure fails closed only for
// the explicitly selected IPs, while unselected IPs never reach this method.
func (s *GatewayIPNoticeSession) Allow(ctx context.Context) (middleware.AllowResult, error) {
	if s.Limiter == nil {
		return middleware.AllowResult{}, fmt.Errorf("IP notice rate limiter is unavailable")
	}
	return s.Limiter.Allow(ctx, "gateway_ip_notice:"+s.IP, s.RPM, time.Minute)
}

// SetGatewayIPNoticeSession attaches the already resolved IP policy.
func SetGatewayIPNoticeSession(c *gin.Context, session *GatewayIPNoticeSession) {
	c.Set(gatewayIPNoticeSessionKey, session)
}

// GatewayClientWebSocket is the downstream capability set used by Responses
// ingress. Both coder/websocket.Conn and the selected-IP decorator implement it.
type GatewayClientWebSocket interface {
	Read(context.Context) (coderws.MessageType, []byte, error)
	Write(context.Context, coderws.MessageType, []byte) error
	Close(coderws.StatusCode, string) error
	CloseNow() error
	SetReadLimit(int64)
}

// WrapGatewayIPNoticeWebSocket leaves ordinary connections completely intact.
// The handshake consumed the first turn's quota before the HTTP upgrade.
func WrapGatewayIPNoticeWebSocket(c *gin.Context, conn *coderws.Conn) GatewayClientWebSocket {
	value, ok := c.Get(gatewayIPNoticeSessionKey)
	session, typed := value.(*GatewayIPNoticeSession)
	if !ok || !typed || session == nil {
		return conn
	}
	return &gatewayIPNoticeWebSocket{GatewayClientWebSocket: conn, session: session,
		transformer: ipnotice.NewEventTransformer(ipnotice.Responses, session.Message)}
}

type gatewayIPNoticeWebSocket struct {
	GatewayClientWebSocket
	session     *GatewayIPNoticeSession
	transformer *ipnotice.EventTransformer
	turns       int
	writeMu     sync.Mutex
}

func (c *gatewayIPNoticeWebSocket) Read(ctx context.Context) (coderws.MessageType, []byte, error) {
	kind, body, err := c.GatewayClientWebSocket.Read(ctx)
	if err != nil || !isGatewayIPNoticeCreate(body) {
		return kind, body, err
	}
	c.turns++
	if c.turns > 1 {
		result, limitErr := c.session.Allow(ctx)
		if limitErr != nil || !result.Allowed {
			payload, marshalErr := json.Marshal(map[string]any{"type": "error", "status_code": 400,
				"error": map[string]any{"type": "ip_notice_rate_limit", "code": "ip_notice_rate_limit", "status_code": 400, "message": c.session.Message}})
			if marshalErr != nil {
				return 0, nil, marshalErr
			}
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if writeErr := c.Write(writeCtx, coderws.MessageText, payload); writeErr != nil {
				return 0, nil, writeErr
			}
			return 0, nil, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "IP notice rate limit exceeded", limitErr)
		}
	}
	body, err = ipnotice.InjectRequest(body, ipnotice.Responses, c.session.Message)
	return kind, body, err
}

func isGatewayIPNoticeCreate(body []byte) bool {
	var event struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(body, &event) == nil && (event.Type == "response.create" || event.Type == "")
}

func (c *gatewayIPNoticeWebSocket) Write(ctx context.Context, kind coderws.MessageType, body []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	for _, frame := range c.transformer.Transform(body) {
		if err := c.GatewayClientWebSocket.Write(ctx, kind, frame); err != nil {
			return err
		}
	}
	return nil
}
