package bitget

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Vendor public verification key, published in bitget-wallet-skill. This is
// public protocol material, never an operator credential or wallet key.
const vendorPublicKey = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAk18NCL9CoiE8OQ588ehJ
hVoCenARvVymahlH3Sw8URZATuZw4k8ZKC8Sf7Zu9i9l3L3K5X4m2I20UENkOBzP
YGCRHk3Dy8SQk/e7ucj/hXJH07yNDJuv1t1nWXRhvwpG8rdW03KpDhJy4pgcAMXl
JYnJYqhfj7HW/urMD0KXw7dLNKyWKBoaGzKkoRvvxTSDHk35cjETcYg6H+bEm+Px
a+GnIJkuN5U2/LfZ4WxgNiIdE2zacHLcFoFsM14jTQdcvPid+6ilY8SQCA3GWc72
n1RudWoTj1ThEUVNWXgcwxLFIdiLCNH1YF7qINdRrjOOCCBBBpr6jdANdI2e4Dcy
DQIDAQAB
-----END PUBLIC KEY-----`

type Client struct {
	http *http.Client
	base string
	key  *rsa.PublicKey
}

func NewClient() *Client {
	block, _ := pem.Decode([]byte(vendorPublicKey))
	key, _ := x509.ParsePKIXPublicKey(block.Bytes)
	return &Client{http: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, base: "https://copenapi.bgwapi.io", key: key.(*rsa.PublicKey)}
}

// Error contains only bounded protocol status. Untrusted response bodies and
// transport diagnostics must never enter logs, Bark, or public API responses.
type Error struct {
	HTTPStatus int
	Code       int
}

func (e *Error) Error() string {
	return fmt.Sprintf("Bitget gasless request failed (HTTP %d, code %d)", e.HTTPStatus, e.Code)
}

func (c *Client) request(ctx context.Context, method, path string, body any, verify bool, out any) error {
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return ErrUnsafeOrder
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(raw))
	if err != nil {
		return ErrUnavailable
	}
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	sum := sha256.Sum256([]byte(method + path + string(raw) + ts))
	for k, v := range map[string]string{"Content-Type": "application/json", "channel": "toc_agent", "brand": "toc_agent", "clientversion": "10.0.0", "language": "en", "token": "toc_agent", "User-Agent": "sub2api-bitget/1", "X-SIGN": "0x" + hex.EncodeToString(sum[:]), "X-TIMESTAMP": ts} {
		req.Header.Set(k, v)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return ErrUnavailable
	}
	if res.StatusCode != http.StatusOK {
		return &Error{HTTPStatus: res.StatusCode}
	}
	var envelope struct {
		Status *int            `json:"status"`
		Code   int             `json:"error_code"`
		Data   json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Status == nil {
		return ErrUnavailable
	}
	if *envelope.Status != 0 {
		return &Error{HTTPStatus: res.StatusCode, Code: envelope.Code}
	}
	if verify {
		a, b, d := res.Header.Get("security-check"), res.Header.Get("security-request-check"), res.Header.Get("security-double-check")
		if !c.verify(a, data) || !c.verify(b, raw) || !c.verify(d, []byte(a+b)) {
			return ErrUnsafeOrder
		}
	}
	if out != nil && (len(envelope.Data) == 0 || json.Unmarshal(envelope.Data, out) != nil) {
		return ErrUnavailable
	}
	if o, ok := out.(*Order); ok && verify {
		o.Attestation = &Attestation{Request: raw, Response: data, ResponseSignature: res.Header.Get("security-check"), RequestSignature: res.Header.Get("security-request-check"), DoubleSignature: res.Header.Get("security-double-check")}
	}
	return nil
}

func VerifyAttestation(o *Order, in Intent) error { return NewClient().verifyAttestation(o, in) }
func (c *Client) verifyAttestation(o *Order, in Intent) error {
	if o == nil || o.Attestation == nil {
		return ErrUnsafeOrder
	}
	a := o.Attestation
	if len(a.Response) > 1<<20 || len(a.Request) > 16<<10 || !c.verify(a.ResponseSignature, a.Response) || !c.verify(a.RequestSignature, a.Request) || !c.verify(a.DoubleSignature, []byte(a.ResponseSignature+a.RequestSignature)) {
		return ErrUnsafeOrder
	}
	var envelope struct {
		Status *int  `json:"status"`
		Data   Order `json:"data"`
	}
	if json.Unmarshal(a.Response, &envelope) != nil || envelope.Status == nil || *envelope.Status != 0 {
		return ErrUnsafeOrder
	}
	copyOrder := *o
	copyOrder.Attestation = nil
	left, _ := json.Marshal(copyOrder)
	right, _ := json.Marshal(envelope.Data)
	var aOrder, bOrder any
	if json.Unmarshal(left, &aOrder) != nil || json.Unmarshal(right, &bOrder) != nil || !reflect.DeepEqual(aOrder, bOrder) {
		return ErrUnsafeOrder
	}
	var req Request
	if json.Unmarshal(a.Request, &req) != nil {
		return ErrUnsafeOrder
	}
	chain, _ := Chain(in.Chain)
	got, e := RawAmount(req.Amount, in.Decimals)
	want, e2 := Uint(in.Net)
	if e != nil || e2 != nil || got.Cmp(want) != 0 || req.Chain != chain || !req.NoGas || !strings.EqualFold(req.From, in.From) || !strings.EqualFold(req.To, in.To) || !strings.EqualFold(req.Contract, in.Token) || !strings.EqualFold(req.PayToken, in.Token) {
		return ErrUnsafeOrder
	}
	// Only the exact public transfer fields are permitted. In particular the
	// caller cannot smuggle override7702 into an otherwise matching request.
	var fields map[string]json.RawMessage
	if json.Unmarshal(a.Request, &fields) != nil || len(fields) != 7 {
		return ErrUnsafeOrder
	}
	for _, key := range []string{"chain", "contract", "from", "to", "amount", "noGas", "noGasPayToken"} {
		if _, ok := fields[key]; !ok {
			return ErrUnsafeOrder
		}
	}
	return nil
}

func (c *Client) verify(signature string, data []byte) bool {
	sig, err := hex.DecodeString(strings.TrimPrefix(signature, "0x"))
	if err != nil || len(sig) == 0 {
		return false
	}
	hash := sha256.Sum256(data)
	return rsa.VerifyPKCS1v15(c.key, crypto.SHA256, hash[:], sig) == nil
}

func (c *Client) Prepare(ctx context.Context, req Request) (*Order, error) {
	var order Order
	if err := c.request(ctx, http.MethodPost, "/userv2/order/makeTransferOrder", req, true, &order); err != nil {
		return nil, err
	}
	if len(order.ID) == 0 || len(order.ID) > 128 {
		return nil, ErrUnsafeOrder
	}
	return &order, nil
}
func (c *Client) Submit(ctx context.Context, id, signed string) error {
	return c.request(ctx, http.MethodPost, "/userv2/order/submitTransferOrder", struct {
		ID  string `json:"orderId"`
		Sig string `json:"sig"`
	}{id, signed}, false, nil)
}
func (c *Client) Status(ctx context.Context, id string) (*OrderStatus, error) {
	var result OrderStatus
	if err := c.request(ctx, http.MethodGet, "/userv2/order/getTransferOrder?orderId="+url.QueryEscape(id), nil, false, &result); err != nil {
		return nil, err
	}
	if result.ID != id {
		return nil, ErrUnsafeOrder
	}
	return &result, nil
}
