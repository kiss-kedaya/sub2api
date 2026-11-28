package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/stretchr/testify/require"
)

// pngBytes is a real 1x1 PNG image so both http.DetectContentType and
// image.DecodeConfig validate it cleanly.
var pngBytes = func() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.NRGBA{R: 12, G: 34, B: 56, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}()

var jpegBytes = func() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 100, G: 150, B: 200, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		panic(err)
	}
	return buf.Bytes()
}()

var gifBytes = func() []byte {
	img := image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.Black, color.White})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		panic(err)
	}
	return buf.Bytes()
}()

// webpBytes is a minimal valid lossless 1x1 WebP (VP8L) image.
var webpBytes = func() []byte {
	raw, err := base64.StdEncoding.DecodeString("UklGRh4AAABXRUJQVlA4TBEAAAAvAAAAAAfQ//73v/+BiOh/AAA=")
	if err != nil {
		panic(err)
	}
	return raw
}()

type savedImage struct {
	key         string
	contentType string
	data        []byte
}

type fakeImageStorage struct {
	saved []savedImage
	url   string
	err   error
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func (f *fakeImageStorage) Save(_ context.Context, key, contentType string, data []byte) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.saved = append(f.saved, savedImage{key: key, contentType: contentType, data: append([]byte(nil), data...)})
	if f.url != "" {
		return f.url, nil
	}
	return "https://cdn.test/" + key, nil
}

func TestImageResultUploaderRewritesB64JSON(t *testing.T) {
	storage := &fakeImageStorage{}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)

	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	result := json.RawMessage(`{"created":1,"data":[{"b64_json":"` + b64 + `","revised_prompt":"a cat"}]}`)

	out, err := uploader.Rewrite(context.Background(), "imgtask_abc", result)
	require.NoError(t, err)

	require.Len(t, storage.saved, 1)
	require.Equal(t, "images/imgtask_abc-0.png", storage.saved[0].key)
	require.Equal(t, "image/png", storage.saved[0].contentType)
	require.Equal(t, pngBytes, storage.saved[0].data)

	var parsed struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out, &parsed))
	require.Len(t, parsed.Data, 1)
	require.JSONEq(t, `"https://cdn.test/images/imgtask_abc-0.png"`, string(parsed.Data[0]["url"]))
	_, hasB64 := parsed.Data[0]["b64_json"]
	require.False(t, hasB64, "b64_json must be stripped after offload")
	require.JSONEq(t, `"a cat"`, string(parsed.Data[0]["revised_prompt"]), "unrelated fields preserved")
}

func TestImageResultUploaderRewritesURL(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer upstream.Close()

	storage := &fakeImageStorage{}
	uploader := NewImageResultUploader(storage, "images/", 0, upstream.Client())

	result := json.RawMessage(`{"created":1,"data":[{"url":"` + upstream.URL + `/pic.png"}]}`)
	out, err := uploader.Rewrite(context.Background(), "imgtask_xyz", result)
	require.NoError(t, err)

	require.Len(t, storage.saved, 1)
	require.Equal(t, pngBytes, storage.saved[0].data)
	require.Equal(t, "image/png", storage.saved[0].contentType)

	var parsed struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out, &parsed))
	require.JSONEq(t, `"https://cdn.test/images/imgtask_xyz-0.png"`, string(parsed.Data[0]["url"]))
}

func TestImageResultUploaderRewritesImageDataURLWithoutHTTP(t *testing.T) {
	httpCalls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return nil, errors.New("HTTP must not be called for data URLs")
	})}
	storage := &fakeImageStorage{}
	uploader := NewImageResultUploader(storage, "images/", 0, client)
	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	result := json.RawMessage(`{"data":[{"url":"DATA:image/jpeg;name=photo.jpg;BaSe64,` + b64 + `","revised_prompt":"kept"}]}`)

	out, err := uploader.Rewrite(context.Background(), "imgtask_data", result)
	require.NoError(t, err)
	require.Zero(t, httpCalls)
	require.Len(t, storage.saved, 1)
	require.Equal(t, pngBytes, storage.saved[0].data)
	require.Equal(t, "image/png", storage.saved[0].contentType, "detected bytes take precedence over a conflicting declaration")
	require.Equal(t, "images/imgtask_data-0.png", storage.saved[0].key)

	var parsed struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out, &parsed))
	require.JSONEq(t, `"https://cdn.test/images/imgtask_data-0.png"`, string(parsed.Data[0]["url"]))
	require.JSONEq(t, `"kept"`, string(parsed.Data[0]["revised_prompt"]))
}

func TestImageResultUploaderDataURLValidation(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{name: "missing comma", url: "data:image/png;base64", wantErr: "missing comma"},
		{name: "non image", url: "data:text/plain;base64,aGVsbG8=", wantErr: "is not an image"},
		{name: "non base64", url: "data:image/png,raw", wantErr: "not base64"},
		{name: "invalid base64", url: "data:image/png;base64,%%%", wantErr: "base64 payload"},
		{name: "invalid media type", url: "data:image/png;bad parameter;base64,aGVsbG8=", wantErr: "invalid media type"},
		{name: "parameter after base64", url: "data:image/png;base64;name=photo.png,aGVsbG8=", wantErr: "base64 marker must be the final header token"},
		{name: "duplicate base64 marker", url: "data:image/png;base64;base64,aGVsbG8=", wantErr: "duplicate base64 marker"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpCalls := 0
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				httpCalls++
				return nil, errors.New("HTTP must not be called for data URLs")
			})}
			uploader := NewImageResultUploader(&fakeImageStorage{}, "images/", 0, client)
			result, err := json.Marshal(map[string]any{"data": []map[string]string{{"url": tt.url}}})
			require.NoError(t, err)

			_, err = uploader.Rewrite(context.Background(), "imgtask_bad", result)
			require.ErrorContains(t, err, tt.wantErr)
			require.Zero(t, httpCalls)
		})
	}
}

func TestImageResultUploaderRejectsOversizedImageDataURL(t *testing.T) {
	storage := &fakeImageStorage{}
	uploader := NewImageResultUploader(storage, "images/", 3, nil)
	payload := base64.StdEncoding.EncodeToString([]byte("four"))
	result := json.RawMessage(`{"data":[{"url":"data:image/png;base64,` + payload + `"}]}`)

	_, err := uploader.Rewrite(context.Background(), "imgtask_large", result)
	require.ErrorContains(t, err, "decoded image data URL exceeds 3 bytes")
	require.Empty(t, storage.saved)
}

func TestImageResultUploaderB64JSONTakesPrecedenceOverDataURL(t *testing.T) {
	storage := &fakeImageStorage{}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)
	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	result := json.RawMessage(`{"data":[{"b64_json":"` + b64 + `","url":"data:text/plain,not-an-image"}]}`)

	_, err := uploader.Rewrite(context.Background(), "imgtask_precedence", result)
	require.NoError(t, err)
	require.Len(t, storage.saved, 1)
	require.Equal(t, pngBytes, storage.saved[0].data)
}

func TestImageResultUploaderPropagatesStorageError(t *testing.T) {
	storage := &fakeImageStorage{err: errors.New("bucket unreachable")}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)

	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	result := json.RawMessage(`{"data":[{"b64_json":"` + b64 + `"}]}`)

	_, err := uploader.Rewrite(context.Background(), "imgtask_err", result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "bucket unreachable")
}

func TestImageResultUploaderNilStoragePassthrough(t *testing.T) {
	var uploader *ImageResultUploader
	result := json.RawMessage(`{"data":[{"url":"https://example.test/x.png"}]}`)
	out, err := uploader.Rewrite(context.Background(), "imgtask_nil", result)
	require.NoError(t, err)
	require.JSONEq(t, string(result), string(out))
}

func TestImageTaskServiceCompleteOffloadsToStorage(t *testing.T) {
	store := &imageTaskMemoryStore{}
	storage := &fakeImageStorage{}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)
	svc := NewImageTaskServiceWithUploader(store, uploader, time.Hour, time.Minute)
	require.True(t, svc.Enabled())

	owner := ImageTaskOwner{UserID: 1, APIKeyID: 2}
	created, err := svc.Create(context.Background(), owner)
	require.NoError(t, err)

	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	result := json.RawMessage(`{"created":1,"data":[{"b64_json":"` + b64 + `"}]}`)
	require.NoError(t, svc.Complete(context.Background(), created.ID, http.StatusOK, result))

	got, err := svc.Get(context.Background(), owner, created.ID)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusCompleted, got.Status)
	require.Equal(t, "https://cdn.test/images/"+created.ID+"-0.png", got.ImageURL)
	require.NotContains(t, string(got.Result), "b64_json", "large base64 must not be persisted to Redis")
	require.Len(t, storage.saved, 1)
}

func TestImageTaskServiceStageBillingOffloadsBeforePrivateCache(t *testing.T) {
	store := &imageTaskMemoryStore{}
	storage := &fakeImageStorage{}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)
	svc := NewImageTaskServiceWithUploader(store, uploader, time.Hour, time.Minute)
	owner := ImageTaskOwner{UserID: 1, APIKeyID: 2}
	created, err := svc.Create(context.Background(), owner)
	require.NoError(t, err)
	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	result := json.RawMessage(`{"data":[{"b64_json":"` + b64 + `"}]}`)

	require.NoError(t, svc.StageBilling(context.Background(), created.ID, http.StatusOK, result))

	require.Equal(t, ImageTaskStatusBilling, store.task.Status)
	require.Empty(t, store.task.Result)
	require.NotContains(t, string(store.task.PendingResult), "b64_json")
	require.Contains(t, string(store.task.PendingResult), "https://cdn.test/images/"+created.ID+"-0.png")
	public, err := svc.Get(context.Background(), owner, created.ID)
	require.NoError(t, err)
	require.Empty(t, public.Result)
	require.Empty(t, public.ImageURL)
	require.Len(t, storage.saved, 1)
}

func TestImageTaskServiceStageBillingOffloadFailureKeepsProcessing(t *testing.T) {
	store := &imageTaskMemoryStore{}
	storage := &fakeImageStorage{err: errors.New("bucket unreachable")}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)
	svc := NewImageTaskServiceWithUploader(store, uploader, time.Hour, time.Minute)
	owner := ImageTaskOwner{UserID: 1, APIKeyID: 2}
	created, err := svc.Create(context.Background(), owner)
	require.NoError(t, err)
	b64 := base64.StdEncoding.EncodeToString(pngBytes)

	err = svc.StageBilling(context.Background(), created.ID, http.StatusOK,
		json.RawMessage(`{"data":[{"b64_json":"`+b64+`"}]}`))

	require.ErrorContains(t, err, "bucket unreachable")
	require.Equal(t, ImageTaskStatusProcessing, store.task.Status)
	require.Empty(t, store.task.Result, "failed offload must not cache the source result")
	require.Empty(t, storage.saved)
}

func TestImageTaskServiceCompleteOffloadFailureMarksFailed(t *testing.T) {
	store := &imageTaskMemoryStore{}
	storage := &fakeImageStorage{err: errors.New("bucket unreachable")}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)
	svc := NewImageTaskServiceWithUploader(store, uploader, time.Hour, time.Minute)

	owner := ImageTaskOwner{UserID: 1, APIKeyID: 2}
	created, err := svc.Create(context.Background(), owner)
	require.NoError(t, err)

	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	result := json.RawMessage(`{"data":[{"b64_json":"` + b64 + `"}]}`)
	require.NoError(t, svc.Complete(context.Background(), created.ID, http.StatusOK, result))

	got, err := svc.Get(context.Background(), owner, created.ID)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusFailed, got.Status)
	require.Equal(t, http.StatusBadGateway, got.HTTPStatus)
	require.Contains(t, string(got.Error), "object storage")
	require.NotContains(t, string(got.Result), "b64_json", "failed offload must not persist base64 to Redis")
}

func TestImageResultUploaderBlocksSSRFAndLoopbackRedirects(t *testing.T) {
	loopbackHits := 0
	loopback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		loopbackHits++
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer loopback.Close()

	storage := &fakeImageStorage{}
	// nil httpClient selects the production outbound-safe default client.
	uploader := NewImageResultUploader(storage, "images/", 0, nil)

	for name, target := range map[string]string{
		"loopback httptest": loopback.URL + "/secret.png",
		"loopback literal":  "http://127.0.0.1:8080/pic.png",
		"aws metadata":      "http://169.254.169.254/latest/meta-data/",
		"cgnat metadata":    "http://100.100.100.200/latest/meta-data/",
		"rfc1918":           "http://10.0.0.1/pic.png",
		"ipv6 loopback":     "http://[::1]:8080/pic.png",
		"file scheme":       "file:///etc/passwd",
	} {
		t.Run(name, func(t *testing.T) {
			result, err := json.Marshal(map[string]any{"data": []map[string]string{{"url": target}}})
			require.NoError(t, err)
			_, err = uploader.Rewrite(context.Background(), "imgtask_ssrf", result)
			require.Error(t, err)
			require.ErrorIs(t, err, urlvalidator.ErrOutboundTargetNotAllowed)
		})
	}
	require.Zero(t, loopbackHits, "blocked SSRF target must never receive an HTTP request")
	require.Empty(t, storage.saved)

	t.Run("dns resolving to private ip blocked", func(t *testing.T) {
		ctx := urlvalidator.WithResolver(context.Background(), func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("10.20.30.40")}, nil
		})
		result := json.RawMessage(`{"data":[{"url":"https://rebind.example.test/a.png"}]}`)
		_, err := uploader.Rewrite(ctx, "imgtask_rebind", result)
		require.ErrorIs(t, err, urlvalidator.ErrOutboundTargetNotAllowed)
	})

	t.Run("302 redirect to loopback or non-http scheme blocked", func(t *testing.T) {
		defaultClient := defaultImageDownloadHTTPClient()
		require.NotNil(t, defaultClient.CheckRedirect)

		// End-to-end redirect test using defaultClient.CheckRedirect with a stub public transport.
		redirectClient := &http.Client{
			CheckRedirect: defaultClient.CheckRedirect,
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Hostname() == "public.example.test" {
					return &http.Response{
						StatusCode: http.StatusFound,
						Header:     http.Header{"Location": []string{loopback.URL + "/internal.png"}},
						Body:       http.NoBody,
						Request:    req,
					}, nil
				}
				return http.DefaultTransport.RoundTrip(req)
			}),
		}
		ctx := urlvalidator.WithResolver(context.Background(), func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://public.example.test/start.png", nil)
		require.NoError(t, err)
		_, err = redirectClient.Do(req)
		require.ErrorIs(t, err, urlvalidator.ErrOutboundTargetNotAllowed)
		require.Zero(t, loopbackHits, "redirect to loopback must be blocked before dialing")

		// Non-http(s) scheme on redirect hop is also rejected.
		badSchemeReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "file:///etc/passwd", nil)
		require.NoError(t, err)
		require.ErrorIs(t, defaultClient.CheckRedirect(badSchemeReq, []*http.Request{req}), urlvalidator.ErrOutboundTargetNotAllowed)
	})
}

func TestImageResultUploaderRejectsSVGAndNonImagesAcrossAllPaths(t *testing.T) {
	svgPayload := []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`)
	htmlPayload := []byte(`<!DOCTYPE html><html><body><script>alert(1)</script></body></html>`)
	jsonPayload := []byte(`{"secret":"aws-metadata-token"}`)
	polyglotPNG := append([]byte("\x89PNG\r\n\x1a\n"), htmlPayload...)

	t.Run("data URL rejects SVG and spoofed non-images", func(t *testing.T) {
		cases := []struct {
			name string
			url  string
		}{
			{"svg data url", "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(svgPayload)},
			{"svg disguised as png data url", "data:image/png;base64," + base64.StdEncoding.EncodeToString(svgPayload)},
			{"html disguised as png data url", "data:image/png;base64," + base64.StdEncoding.EncodeToString(htmlPayload)},
			{"json disguised as png data url", "data:image/png;base64," + base64.StdEncoding.EncodeToString(jsonPayload)},
			{"png magic prefix polyglot", "data:image/png;base64," + base64.StdEncoding.EncodeToString(polyglotPNG)},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				storage := &fakeImageStorage{}
				uploader := NewImageResultUploader(storage, "images/", 0, nil)
				raw, err := json.Marshal(map[string]any{"data": []map[string]string{{"url": tc.url}}})
				require.NoError(t, err)
				_, err = uploader.Rewrite(context.Background(), "imgtask_svg", raw)
				require.Error(t, err)
				require.Empty(t, storage.saved)
			})
		}
	})

	t.Run("b64_json rejects SVG, HTML, JSON, polyglot and oversized payloads", func(t *testing.T) {
		for name, payload := range map[string][]byte{
			"svg":      svgPayload,
			"html":     htmlPayload,
			"json":     jsonPayload,
			"polyglot": polyglotPNG,
		} {
			t.Run(name, func(t *testing.T) {
				storage := &fakeImageStorage{}
				uploader := NewImageResultUploader(storage, "images/", 0, nil)
				raw, err := json.Marshal(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(payload)}}})
				require.NoError(t, err)
				_, err = uploader.Rewrite(context.Background(), "imgtask_b64_bad", raw)
				require.Error(t, err)
				require.Empty(t, storage.saved)
			})
		}

		// Oversized b64_json is rejected before saving.
		storage := &fakeImageStorage{}
		uploader := NewImageResultUploader(storage, "images/", 8, nil)
		raw, err := json.Marshal(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngBytes)}}})
		require.NoError(t, err)
		_, err = uploader.Rewrite(context.Background(), "imgtask_b64_big", raw)
		require.ErrorContains(t, err, "exceeds 8 bytes")
		require.Empty(t, storage.saved)
	})

	t.Run("HTTP download rejects SVG, HTML, JSON even with spoofed image Content-Type", func(t *testing.T) {
		cases := []struct {
			name        string
			contentType string
			body        []byte
		}{
			{"svg header and body", "image/svg+xml", svgPayload},
			{"svg with spoofed png header", "image/png", svgPayload},
			{"html with spoofed png header", "image/png", htmlPayload},
			{"json with text/plain header", "application/json", jsonPayload},
			{"png magic polyglot", "image/png", polyglotPNG},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", tc.contentType)
					_, _ = w.Write(tc.body)
				}))
				defer server.Close()

				storage := &fakeImageStorage{}
				uploader := NewImageResultUploader(storage, "images/", 0, server.Client())
				raw, err := json.Marshal(map[string]any{"data": []map[string]string{{"url": server.URL + "/bad"}}})
				require.NoError(t, err)
				_, err = uploader.Rewrite(context.Background(), "imgtask_dl_bad", raw)
				require.Error(t, err)
				require.Empty(t, storage.saved)
			})
		}
	})
}

func TestImageResultUploaderAcceptsValidBitmapFormats(t *testing.T) {
	formats := []struct {
		name     string
		bytes    []byte
		wantMIME string
		wantExt  string
	}{
		{"png", pngBytes, "image/png", ".png"},
		{"jpeg", jpegBytes, "image/jpeg", ".jpg"},
		{"gif", gifBytes, "image/gif", ".gif"},
		{"webp", webpBytes, "image/webp", ".webp"},
	}

	for _, tc := range formats {
		t.Run("b64_json/"+tc.name, func(t *testing.T) {
			storage := &fakeImageStorage{}
			uploader := NewImageResultUploader(storage, "images/", 0, nil)
			raw, err := json.Marshal(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(tc.bytes)}}})
			require.NoError(t, err)
			_, err = uploader.Rewrite(context.Background(), "imgtask_ok", raw)
			require.NoError(t, err)
			require.Len(t, storage.saved, 1)
			require.Equal(t, tc.wantMIME, storage.saved[0].contentType)
			require.Equal(t, "images/imgtask_ok-0"+tc.wantExt, storage.saved[0].key)
		})

		t.Run("download/"+tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				// Even when upstream sends generic octet-stream, valid bitmap bytes are accepted.
				w.Header().Set("Content-Type", "application/octet-stream")
				_, _ = w.Write(tc.bytes)
			}))
			defer server.Close()

			storage := &fakeImageStorage{}
			uploader := NewImageResultUploader(storage, "images/", 0, server.Client())
			raw, err := json.Marshal(map[string]any{"data": []map[string]string{{"url": server.URL + "/img"}}})
			require.NoError(t, err)
			_, err = uploader.Rewrite(context.Background(), "imgtask_dl_ok", raw)
			require.NoError(t, err)
			require.Len(t, storage.saved, 1)
			require.Equal(t, tc.wantMIME, storage.saved[0].contentType)
			require.Equal(t, "images/imgtask_dl_ok-0"+tc.wantExt, storage.saved[0].key)
		})
	}
}
