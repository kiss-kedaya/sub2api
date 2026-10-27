package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func (s *OpenAIGatewayService) SetExcelBPSChannel(channel *ExcelBPSChannel) {
	s.excelBPSChannel = channel
}

func (s *OpenAIGatewayService) forwardSelectedExcelBPS(ctx context.Context, c *gin.Context, account *Account, body []byte, start time.Time, clientModel string) (*OpenAIForwardResult, bool, error) {
	if !ExcelBPSRouteSelected(account, gjson.GetBytes(body, "model").String()) {
		return nil, false, nil
	}
	if s.excelBPSChannel == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "service_unavailable", "message": "Excel protocol is unavailable"}})
		return nil, true, fmt.Errorf("excel protocol is unavailable")
	}
	session := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String())
	if session == "" {
		session = strings.TrimSpace(gjson.GetBytes(body, "session_id").String())
	}
	if session == "" {
		session = strings.TrimSpace(c.GetHeader("session_id"))
	}
	prepared, err := s.excelBPSChannel.Prepare(ctx, account, body, getAPIKeyIDFromContext(c), session)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "Excel protocol configuration or conversation state is unavailable"}})
		return nil, true, err
	}
	if prepared.NativeFallback {
		return nil, false, nil
	}
	clientCtx := ctx
	if c.Request != nil {
		clientCtx = c.Request.Context()
	}
	if err := clientCtx.Err(); err != nil {
		return nil, true, err
	}
	upstreamCtx, releaseUpstream := detachUpstreamContext(ctx)
	defer releaseUpstream()
	proxy := ""
	if account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	SetActualOpenAIUpstreamEndpoint(c, "/basispoints/api/responses")
	SetOpsUpstreamModel(c, gjson.GetBytes(body, "model").String())
	// BPS has its own resource-server grant. Codex OAuth plugins, ticket
	// acquisition and cookie injection must never run on this request.
	upstreamStart := time.Now()
	request := prepared.Request.WithContext(WithHTTPUpstreamProfile(upstreamCtx, HTTPUpstreamProfileOpenAI))
	resp, err := s.doOpenAIRequestWithHeaderCancel(clientCtx, request, func(req *http.Request) (*http.Response, error) {
		return s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	})
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	captureOpsHeadersFromRequest(request, resp)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if clientCtx.Err() != nil {
			return nil, true, clientCtx.Err()
		}
		s.recordOpenAIStreamUpstreamError(c, account, true, "", "transport_error", nil, "Excel upstream transport failed")
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "Excel upstream transport failed; turn was not replayed"}})
		return nil, true, err
	}
	if resp == nil || resp.Body == nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "Excel upstream returned no response"}})
		return nil, true, fmt.Errorf("excel upstream returned no response")
	}
	upstreamResponse := resp
	defer func() { _ = upstreamResponse.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusUnauthorized {
			s.excelBPSChannel.grants.Invalidate(account.ID)
		}
		s.recordOpenAIStreamUpstreamError(c, account, true, resp.Header.Get("x-request-id"), "http_error", nil, "Excel upstream rejected this turn")
		c.JSON(resp.StatusCode, gin.H{"error": gin.H{"type": "upstream_error", "message": "Excel upstream rejected this turn"}})
		return nil, true, fmt.Errorf("excel upstream status %d", resp.StatusCode)
	}
	resp, err = prepared.Transform(upstreamCtx, resp)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "Excel response could not be converted"}})
		return nil, true, err
	}
	// Conversion owns a pipe above the resource-server body. Give both stream
	// and buffered clients the normal bounded usage-drain window on cancel.
	resp.Body = s.openAIBufferedBodyWithCancel(clientCtx, resp.Body)
	defer func() { _ = resp.Body.Close() }()
	SetActualOpenAIUpstreamEndpoint(c, "/basispoints/api/responses")
	model, stream := gjson.GetBytes(body, "model").String(), gjson.GetBytes(body, "stream").Bool()
	if clientModel == "" {
		clientModel = model
	}
	result := &OpenAIForwardResult{Model: model, UpstreamModel: model, Stream: stream, RequestID: resp.Header.Get("x-request-id"), UpstreamEndpoint: "/basispoints/api/responses", UpstreamHeaders: resp.Header.Clone()}
	if stream {
		v, relayErr := s.handleStreamingResponsePassthrough(ctx, resp, c, account, start, clientModel, model)
		if v != nil {
			if v.usage != nil {
				result.Usage = *v.usage
			}
			result.ResponseID, result.FirstTokenMs = v.responseID, v.firstTokenMs
			result.ClientDisconnect = v.clientDisconnect
			result.NonBillableUpstreamError = v.nonBillableUpstreamError
			result.ImageCount = v.imageCount
			result.ImageOutputSizes = append([]string{}, v.imageOutputSizes...)
		}
		err = relayErr
	} else {
		// The shared buffered handler returns nil on a failed SSE terminal. Keep
		// provider-reported usage independently so a failed billed turn does not
		// silently lose its metered usage.
		tap := &excelBPSUsageBody{ReadCloser: resp.Body, parse: s.parseSSEUsageBytesWithType}
		resp.Body = tap
		v, relayErr := s.handleNonStreamingResponsePassthrough(ctx, resp, c, account, clientModel, model)
		if v != nil {
			if v.usage != nil {
				result.Usage = *v.usage
			}
			result.ResponseID = v.responseID
			result.NonBillableUpstreamError = v.nonBillableUpstreamError
			result.ImageCount = v.imageCount
			result.ImageOutputSizes = append([]string{}, v.imageOutputSizes...)
		}
		if v == nil {
			result.Usage = tap.usage
		}
		err = relayErr
	}
	result.Duration = time.Since(start)
	if observer := upstreamResponseModelObserverFromContext(c); observer != nil {
		result.UpstreamResponseModel = observer.Model()
		result.UpstreamResponseModelConflict = observer.Conflict()
		result.UpstreamResponseServiceTier = observer.ServiceTier()
	}
	if err != nil {
		if !c.Writer.Written() && !IsResponseCommitted(c) {
			status := http.StatusBadGateway
			var failover *UpstreamFailoverError
			if errors.As(err, &failover) && failover.StatusCode >= 400 && failover.StatusCode <= 599 {
				status = failover.StatusCode
			}
			c.JSON(status, gin.H{"error": gin.H{"type": "upstream_error", "message": "Excel upstream did not complete this turn"}})
		}
		if errors.Is(err, context.Canceled) {
			return result, true, fmt.Errorf("excel relay canceled: %w", context.Canceled)
		}
		// Shared relay can return a typed failover after a generation has been
		// accepted. Do not replay that paid generation on another account.
		return result, true, fmt.Errorf("excel relay failed: %v", err)
	}
	return result, true, nil
}

type excelBPSUsageBody struct {
	io.ReadCloser
	pending []byte
	usage   OpenAIUsage
	parse   func([]byte, string, *OpenAIUsage)
}

func (b *excelBPSUsageBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.pending = append(b.pending, p[:n]...)
		for {
			at := bytes.IndexByte(b.pending, '\n')
			if at < 0 {
				break
			}
			line := bytes.TrimSuffix(b.pending[:at], []byte{'\r'})
			if bytes.HasPrefix(line, []byte("data:")) {
				data := bytes.TrimSpace(line[5:])
				b.parse(data, gjson.GetBytes(data, "type").String(), &b.usage)
			}
			b.pending = b.pending[at+1:]
		}
		if len(b.pending) > 16<<20 {
			b.pending = nil
		}
	}
	return n, err
}
