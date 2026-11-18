//go:build unit

package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeminiR6ChatPreludeRecovery(t *testing.T) {
	for _, text := range []string{"", "synthetic recovered answer"} {
		for _, finish := range []string{"stop", "length", "error", ""} {
			t.Run(fmt.Sprintf("text=%q/finish=%s", text, finish), func(t *testing.T) {
				body := "data: {\"choices\":[],\"usage\":" + geminiBridgeChatUsage + "}\n\n" +
					"data: {\"error\":\"synthetic transient prelude\"}\n\n" +
					fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%q},\"finish_reason\":null}]}\n\n", text)
				if finish != "" {
					body += fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":%q}]}\n\n", finish)
				}
				body += "data: [DONE]\n\n"
				result, err, output := geminiR4BridgeForward(t, APIProtocolChatCompletions, true, body)
				success := finish == "stop" || finish == "length"
				if success {
					require.NoError(t, err, output)
					require.NotContains(t, output, `"error"`)
					require.Contains(t, output, `"finishReason":"`+geminiBridgeFinishReason(finish)+`"`)
					if text != "" {
						require.Contains(t, output, text)
					}
				} else {
					require.Error(t, err)
					require.Equal(t, 1, strings.Count(output, `"error"`))
					require.NotContains(t, output, `"finishReason"`)
				}
				requireGeminiBridgeBilling(t, result, geminiBridgeAccount(APIProtocolChatCompletions), !success)
			})
		}
	}
}

func TestGeminiR6ChatLaterErrorKeepsBillingFailure(t *testing.T) {
	body := "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"error\":\"synthetic late failure\"}\n\n" +
		"data: {\"choices\":[],\"usage\":" + geminiBridgeChatUsage + "}\n\ndata: [DONE]\n\n"
	result, err, output := geminiR4BridgeForward(t, APIProtocolChatCompletions, true, body)
	require.Error(t, err)
	require.Equal(t, 1, strings.Count(output, `"error"`))
	require.NotContains(t, output, `"finishReason"`)
	requireGeminiBridgeBilling(t, result, geminiBridgeAccount(APIProtocolChatCompletions), true)
}

func TestGeminiR6NativeLastEventFieldWins(t *testing.T) {
	for _, headers := range []string{
		"event: error\nevent: message\n",
		"event: error\nevent: message\nevent: message\n",
		"event: error\nevent:\n",
		"event: message\nevent: error\n",
	} {
		for _, crlf := range []bool{false, true} {
			t.Run(fmt.Sprintf("%q/crlf=%t", headers, crlf), func(t *testing.T) {
				body := headers + "data: " + geminiB2Success + "\n\n"
				if crlf {
					body = strings.ReplaceAll(body, "\n", "\r\n")
				}
				result, err, _, rec := geminiGaps3Forward(t, true, "stream", body, nil)
				require.NoError(t, err)
				var parser openAICompatSSEFrameParser
				var events []openAICompatSSEFrame
				for _, line := range strings.Split(strings.ReplaceAll(rec.Body.String(), "\r\n", "\n"), "\n") {
					if frame, ok := parser.AddLine(line); ok {
						events = append(events, frame)
					}
				}
				require.Len(t, events, 1, rec.Body.String())
				last := "message"
				if strings.HasSuffix(headers, "event:\n") {
					last = ""
				}
				if strings.HasSuffix(headers, "event: error\n") {
					last = "error"
				}
				require.Equal(t, last, events[0].EventType, rec.Body.String())
				requireGeminiSignalBilling(t, result, last == "error")
			})
		}
	}
}
