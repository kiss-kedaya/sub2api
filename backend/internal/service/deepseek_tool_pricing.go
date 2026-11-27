package service

import (
	"bytes"
	"context"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"strings"
)

const (
	deepSeekOCRMaxEstimatedPages = 1000
	deepSeekOCRBytesPerPageEst   = 100 << 10 // 100 KiB fallback when PDF pages live in compressed object streams
	deepSeekOCRStreamBufSize     = 32 << 10  // 32 KiB fixed buffer so 100 MiB uploads never duplicate in RAM
	deepSeekOCRPDFScanTailBytes  = 64        // overlap across chunks for `/Type <ws> /Page<delim>`
)

// DeepSeekToolCatalogOrder defines the canonical display order for DeepSeek
// standalone tool endpoints in Model Plaza and Model Comparison.
var DeepSeekToolCatalogOrder = []string{
	DeepSeekToolsLabelSearch,
	DeepSeekToolsLabelFetch,
	DeepSeekToolsLabelOCR,
	DeepSeekToolsLabelTTS,
	DeepSeekToolsLabelASR,
}

// IsDeepSeekToolLabel reports whether model is one of the five DeepSeek tool
// billing labels.
func IsDeepSeekToolLabel(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case DeepSeekToolsLabelSearch,
		DeepSeekToolsLabelFetch,
		DeepSeekToolsLabelOCR,
		DeepSeekToolsLabelTTS,
		DeepSeekToolsLabelASR:
		return true
	default:
		return false
	}
}

// DeepSeekToolOfficialUnitPrice returns the built-in official DeepSeek base
// unit price in each tool's canonical billing unit:
//   - search / fetch: per call (5.0 / 1,000 calls = 0.005)
//   - ocr: per page (1.5 / 1,000 pages = 0.0015)
//   - tts: per 1,000,000 billable chars (300.0)
//   - asr: per hour (0.78)
func DeepSeekToolOfficialUnitPrice(model string) (float64, bool) {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case DeepSeekToolsLabelSearch, DeepSeekToolsLabelFetch:
		return defaultSearchPricePer1k / 1000.0, true
	case DeepSeekToolsLabelOCR:
		return defaultOCRPricePer1kPages / 1000.0, true
	case DeepSeekToolsLabelTTS:
		return defaultDeepSeekTTSPricePerMillionChars, true
	case DeepSeekToolsLabelASR:
		return defaultDeepSeekASRPricePerHour, true
	default:
		return 0, false
	}
}

func validNonNegativeFinitePrice(ptr *float64) (float64, bool) {
	if ptr == nil {
		return 0, false
	}
	v := *ptr
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0, false
	}
	return v, true
}

// matchGroupPerRequestPrice matches only an exact tool model name on a group's
// per_request pricing cards. DeepSeek tools span heterogeneous units (call,
// page, 1M chars, hour), so wildcard patterns like "deepseek-*" must not match.
func matchGroupPerRequestPrice(group *Group, model string) *float64 {
	if group == nil {
		return nil
	}
	target := normalizeChannelPricingModelName(model)
	for i := range group.ModelPricing {
		entry := &group.ModelPricing[i]
		if entry.BillingMode != BillingModePerRequest {
			continue
		}
		for _, pattern := range entry.Models {
			if normalizeChannelPricingModelName(pattern) == target {
				if v, ok := validNonNegativeFinitePrice(entry.PerRequestPrice); ok {
					return &v
				}
				return nil
			}
		}
	}
	return nil
}

// ResolveDeepSeekToolUnitPrice resolves the base unit price (before customer
// multiplier) for any of the five DeepSeek tool labels using one shared
// precedence chain across billing, inflight reservation, and Model Plaza:
//
//   - search / ocr / tts / asr:
//     Group model_pricing per_request card > Group dedicated column >
//     Channel model_pricing per_request card > built-in official default.
//   - fetch:
//     Group fetch card > Group fetch_price_per_1k > Channel fetch card >
//     Group search card > Group search_price_per_1k > Channel search card >
//     built-in official default (5.0 / 1,000 calls).
//
// An explicit 0 at any level means free and stops fallback.
func ResolveDeepSeekToolUnitPrice(
	ctx context.Context,
	resolver *ModelPricingResolver,
	group *Group,
	model string,
) (float64, bool) {
	canonical := strings.ToLower(strings.TrimSpace(model))
	resolveDirect := func(target string, groupDedicated *float64, scale float64) (float64, bool) {
		if p := matchGroupPerRequestPrice(group, target); p != nil {
			return *p, true
		}
		if v, ok := validNonNegativeFinitePrice(groupDedicated); ok {
			return v / scale, true
		}
		if resolver != nil {
			if p := resolver.resolveChannelPerRequestPrice(ctx, group, target); p != nil {
				return *p, true
			}
		}
		return 0, false
	}

	var searchPer1k, fetchPer1k, ocrPer1k, ttsPerM, asrPerHour *float64
	if group != nil {
		searchPer1k = group.SearchPricePer1k
		fetchPer1k = group.FetchPricePer1k
		ocrPer1k = group.OCRPricePer1kPages
		ttsPerM = group.AudioTTSPricePerMillionChars
		asrPerHour = group.AudioSTTPricePerHour
	}

	switch canonical {
	case DeepSeekToolsLabelSearch:
		if p, ok := resolveDirect(DeepSeekToolsLabelSearch, searchPer1k, 1000.0); ok {
			return p, true
		}
		return defaultSearchPricePer1k / 1000.0, true
	case DeepSeekToolsLabelFetch:
		if p, ok := resolveDirect(DeepSeekToolsLabelFetch, fetchPer1k, 1000.0); ok {
			return p, true
		}
		if p, ok := resolveDirect(DeepSeekToolsLabelSearch, searchPer1k, 1000.0); ok {
			return p, true
		}
		return defaultSearchPricePer1k / 1000.0, true
	case DeepSeekToolsLabelOCR:
		if p, ok := resolveDirect(DeepSeekToolsLabelOCR, ocrPer1k, 1000.0); ok {
			return p, true
		}
		return defaultOCRPricePer1kPages / 1000.0, true
	case DeepSeekToolsLabelTTS:
		if p, ok := resolveDirect(DeepSeekToolsLabelTTS, ttsPerM, 1.0); ok {
			return p, true
		}
		return defaultDeepSeekTTSPricePerMillionChars, true
	case DeepSeekToolsLabelASR:
		if p, ok := resolveDirect(DeepSeekToolsLabelASR, asrPerHour, 1.0); ok {
			return p, true
		}
		return defaultDeepSeekASRPricePerHour, true
	default:
		return 0, false
	}
}

// CalculateDeepSeekToolCost computes TotalCost (base unit price × units) and
// ActualCost (TotalCost × rateMultiplier) for a DeepSeek tool invocation.
func CalculateDeepSeekToolCost(
	ctx context.Context,
	resolver *ModelPricingResolver,
	group *Group,
	model string,
	units float64,
	rateMultiplier float64,
) *CostBreakdown {
	out := &CostBreakdown{BillingMode: string(BillingModePerRequest)}
	if units <= 0 || math.IsNaN(units) || math.IsInf(units, 0) {
		return out
	}
	unitPrice, ok := ResolveDeepSeekToolUnitPrice(ctx, resolver, group, model)
	if !ok || unitPrice <= 0 || math.IsNaN(unitPrice) || math.IsInf(unitPrice, 0) {
		return out
	}
	if rateMultiplier < 0 || math.IsNaN(rateMultiplier) || math.IsInf(rateMultiplier, 0) {
		rateMultiplier = 0
	}
	total := unitPrice * units
	out.TotalCost = total
	out.ActualCost = total * rateMultiplier
	return out
}

func isDeepSeekStandaloneSearchOrFetch(result *OpenAIForwardResult, apiKey *APIKey) bool {
	if result == nil {
		return false
	}
	if result.Model == DeepSeekToolsLabelSearch || result.Model == DeepSeekToolsLabelFetch {
		return true
	}
	return result.FixedToolBilling && apiKey != nil && apiKey.Group != nil && apiKey.Group.Platform == PlatformDeepseek
}

// EstimateDeepSeekOCRPages returns a conservative page-count estimate in
// [1, 1000] for inflight balance reservation on /v1/tools/ocr.
// Single-page raster images (PNG/JPEG/WebP/GIF/BMP) reserve 1 page instead of
// the worst-case 1,000-page ceiling; PDFs stream-scan uncompressed /Type /Page
// objects in fixed 32 KiB chunks (zero io.ReadAll heap duplication on 100 MiB
// uploads) with a size-based fallback for compressed object streams.
func EstimateDeepSeekOCRPages(contentType string, body []byte) int {
	if len(body) == 0 {
		return 1
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return estimateDeepSeekOCRPart(bytes.NewReader(body)).pages()
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var bestFile ocrPartEstimate
	var largest ocrPartEstimate
	for {
		part, partErr := reader.NextPart()
		if partErr == io.EOF {
			break
		}
		if partErr != nil {
			if bestFile.sizeBytes > 0 {
				return bestFile.pages()
			}
			if largest.sizeBytes > 0 {
				return largest.pages()
			}
			return estimateDeepSeekOCRPart(bytes.NewReader(body)).pages()
		}
		est := estimateDeepSeekOCRPart(part)
		_ = part.Close()
		if est.sizeBytes == 0 {
			continue
		}
		if part.FileName() != "" {
			if part.FormName() == "file" && (est.isImage || est.pdfPages >= deepSeekOCRMaxEstimatedPages) {
				return est.pages()
			}
			if est.sizeBytes > bestFile.sizeBytes {
				bestFile = est
			}
		}
		if est.sizeBytes > largest.sizeBytes {
			largest = est
		}
	}
	if bestFile.sizeBytes > 0 {
		return bestFile.pages()
	}
	if largest.sizeBytes > 0 {
		return largest.pages()
	}
	return estimateDeepSeekOCRPart(bytes.NewReader(body)).pages()
}

type ocrPartEstimate struct {
	sizeBytes int64
	isImage   bool
	pdfPages  int
}

func (e ocrPartEstimate) pages() int {
	if e.sizeBytes <= 0 || e.isImage {
		return 1
	}
	if e.pdfPages > 0 {
		return clampDeepSeekOCRPages(e.pdfPages)
	}
	estimated := int((e.sizeBytes + deepSeekOCRBytesPerPageEst - 1) / deepSeekOCRBytesPerPageEst)
	return clampDeepSeekOCRPages(estimated)
}

func clampDeepSeekOCRPages(pages int) int {
	if pages < 1 {
		return 1
	}
	if pages > deepSeekOCRMaxEstimatedPages {
		return deepSeekOCRMaxEstimatedPages
	}
	return pages
}

func estimateDeepSeekOCRPart(r io.Reader) ocrPartEstimate {
	buf := make([]byte, deepSeekOCRStreamBufSize)
	// Read up to 1 KiB header first so small/fragmented reads still reveal magic bytes.
	headN := 0
	var readErr error
	for headN < 1024 && readErr == nil {
		n, err := r.Read(buf[headN:])
		headN += n
		readErr = err
		if n == 0 && err == nil {
			break
		}
	}
	if headN == 0 {
		return ocrPartEstimate{}
	}
	if isSinglePageRasterImage(buf[:headN]) {
		return ocrPartEstimate{sizeBytes: int64(headN), isImage: true}
	}
	if bytes.Contains(buf[:min(headN, 1024)], []byte("%PDF-")) {
		return streamEstimatePDFPart(buf[:headN], readErr, r, buf)
	}
	total := int64(headN)
	if readErr == nil {
		if n, err := io.CopyBuffer(io.Discard, r, buf); err == nil || err == io.EOF {
			total += n
		}
	}
	return ocrPartEstimate{sizeBytes: total}
}

func streamEstimatePDFPart(initial []byte, initialErr error, r io.Reader, scratch []byte) ocrPartEstimate {
	window := make([]byte, 0, deepSeekOCRStreamBufSize+deepSeekOCRPDFScanTailBytes)
	window = append(window, initial...)
	total := int64(len(initial))
	pages := 0
	err := initialErr

	for {
		isEOF := err != nil
		safeEnd := len(window)
		if !isEOF {
			safeEnd = len(window) - deepSeekOCRPDFScanTailBytes
			if safeEnd < 0 {
				safeEnd = 0
			}
		}
		if safeEnd > 0 {
			pages += countPDFPagesInRange(window, safeEnd, deepSeekOCRMaxEstimatedPages-pages)
			if pages >= deepSeekOCRMaxEstimatedPages {
				return ocrPartEstimate{sizeBytes: total, pdfPages: deepSeekOCRMaxEstimatedPages}
			}
			rem := len(window) - safeEnd
			copy(window, window[safeEnd:])
			window = window[:rem]
		}
		if isEOF {
			break
		}
		n, readErr := r.Read(scratch)
		if n > 0 {
			window = append(window, scratch[:n]...)
			total += int64(n)
		}
		err = readErr
		if n == 0 && err == nil {
			break
		}
	}
	return ocrPartEstimate{sizeBytes: total, pdfPages: pages}
}

func isSinglePageRasterImage(data []byte) bool {
	// PNG
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) {
		return true
	}
	// JPEG
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return true
	}
	// WebP
	if len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")) {
		return true
	}
	// GIF
	if len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))) {
		return true
	}
	// BMP
	if len(data) >= 2 && data[0] == 'B' && data[1] == 'M' {
		return true
	}
	return false
}

// countPDFPagesInRange scans data for `/Type <ws> /Page` matches whose `/Type`
// starts strictly before safeEnd (while allowing lookahead up to len(data)),
// excluding `/Type /Pages`, `/Type /PageLabel`, etc.
func countPDFPagesInRange(data []byte, safeEnd int, limit int) int {
	if limit <= 0 || safeEnd <= 0 {
		return 0
	}
	needle := []byte("/Type")
	pageToken := []byte("/Page")
	count := 0
	offset := 0
	for offset < safeEnd {
		idx := bytes.Index(data[offset:], needle)
		if idx < 0 || offset+idx >= safeEnd {
			break
		}
		matchStart := offset + idx
		pos := matchStart + len(needle)
		for pos < len(data) && isPDFWhitespace(data[pos]) {
			pos++
		}
		if pos+len(pageToken) <= len(data) && bytes.Equal(data[pos:pos+len(pageToken)], pageToken) {
			nextIdx := pos + len(pageToken)
			if nextIdx == len(data) || !isPDFNameChar(data[nextIdx]) {
				count++
				if count >= limit {
					return limit
				}
			}
		}
		offset = matchStart + len(needle)
	}
	return count
}

func isPDFWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\f' || b == 0x00
}

func isPDFNameChar(b byte) bool {
	if isPDFWhitespace(b) {
		return false
	}
	switch b {
	case '/', '<', '>', '[', ']', '(', ')', '{', '}', '%':
		return false
	default:
		return true
	}
}
