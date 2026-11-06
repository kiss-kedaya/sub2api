package service

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"math"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	xdraw "golang.org/x/image/draw"
)

const (
	// The official Images API documents 2-16 frames and 20-2000 ms per frame for
	// GIF output. Values outside that range are left untouched so the upstream
	// keeps owning format and range validation.
	openAIGIFMinFrameCount      = 2
	openAIGIFMaxFrameCount      = 16
	openAIGIFMinFrameDurationMS = 20
	openAIGIFMaxFrameDurationMS = 2000
	openAIGIFDurationRoundToMS  = 10
	// A relay that animates GIFs by packing every frame into one square sheet
	// cannot exceed 3840px on either edge. The sheet side is estimated as
	// ceil(sqrt(frame_count)) — the 4x4 packing used for 16 frames — because the
	// exact layout is upstream-owned; this deliberately rejects the conservative
	// upper bound instead of silently shrinking the requested size.
	openAIImagesGIFSheetMaxEdge   = 3840
	openAIImagesGIFSheetMaxFrames = 16
	// Decoding an animation costs roughly one byte per pixel per frame. Cap the
	// work so a degenerate upstream GIF cannot pin an image slot with hundreds of
	// megapixels; the request itself is already bounded by the upstream body
	// limit.
	maxOpenAIGIFAnimationPixels = 96 << 20
)

// openAIImagesGIFSheetSide is the conservative square-sheet side for frameCount:
// 16 frames -> 4, 8/12 frames -> 3/4, 4 frames -> 2, 2 frames -> 2.
func openAIImagesGIFSheetSide(frameCount int) int {
	if frameCount <= 1 {
		return max(frameCount, 1)
	}
	side := int(math.Ceil(math.Sqrt(float64(frameCount))))
	if side < 1 {
		side = 1
	}
	return side
}

// validateOpenAIImagesGIFSheetLimit rejects a GIF request whose frame count and
// per-frame size cannot fit the upstream sheet before it is sent. It is scoped
// to API-key accounts with a non-official base_url because the sheet packing is
// a relay behavior; official OpenAI and OAuth paths keep their existing
// upstream-owned validation (the sheet limit there is 未验证).
//
// The estimate is conservative: sheet side = ceil(sqrt(frame_count)), and the
// per-frame longest edge is the requested size's longest edge. A request is
// rejected when sheet_side × longest_edge > 3840. The returned message carries
// concrete alternatives (largest per-frame size for the requested frame count,
// and largest frame count at the requested size) and never silently clamps.
func validateOpenAIImagesGIFSheetLimit(parsed *OpenAIImagesRequest, account *Account) error {
	if parsed == nil || account == nil || parsed.FrameCount == nil {
		return nil
	}
	frameCount := *parsed.FrameCount
	if frameCount < openAIGIFMinFrameCount || frameCount > openAIImagesGIFSheetMaxFrames {
		// Out-of-range frame counts stay upstream-owned, matching the GIF repair
		// path in this file.
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(parsed.OutputFormat), "gif") {
		return nil
	}
	if account.Type != AccountTypeAPIKey || isOfficialOpenAIModelsBaseURL(account.GetOpenAIBaseURL()) {
		return nil
	}
	width, height, ok := openAIGIFTargetFrameSize(parsed.Size)
	if !ok {
		// "auto" and non-pixel values cannot be bounded locally.
		return nil
	}
	size := strings.TrimSpace(parsed.Size)
	longest := max(width, height)
	if longest > openAIImagesGIFSheetMaxEdge {
		return fmt.Errorf(
			"size %q has a %dpx longest edge, which exceeds the upstream %dpx GIF sheet limit; frame_count=%d cannot be generated at this size; use a size whose longest edge is at most %dpx",
			size, longest, openAIImagesGIFSheetMaxEdge, frameCount, openAIImagesGIFSheetMaxEdge,
		)
	}
	sheetSide := openAIImagesGIFSheetSide(frameCount)
	if sheetSide*longest <= openAIImagesGIFSheetMaxEdge {
		return nil
	}

	maxFrameEdge := openAIImagesGIFSheetMaxEdge / sheetSide
	scale := float64(maxFrameEdge) / float64(longest)
	maxWidth := max(1, int(math.Floor(float64(width)*scale)))
	maxHeight := max(1, int(math.Floor(float64(height)*scale)))
	// Largest square frame count that still fits at the requested size:
	// floor(limit / longest_edge)² but never above the documented 16 frames.
	edgeCount := openAIImagesGIFSheetMaxEdge / longest
	maxFramesAtSize := edgeCount * edgeCount
	if maxFramesAtSize > openAIImagesGIFSheetMaxFrames {
		maxFramesAtSize = openAIImagesGIFSheetMaxFrames
	}
	if maxFramesAtSize < 1 {
		maxFramesAtSize = 1
	}
	frameWord := "frames"
	if maxFramesAtSize == 1 {
		frameWord = "frame"
	}
	return fmt.Errorf(
		"frame_count=%d at size %q needs a %dx%d GIF sheet (%dpx per edge), which exceeds the upstream limit of %dpx per edge; use at most %dx%d per frame for %d frames, or at most %d %s at %s",
		frameCount, size, sheetSide, sheetSide, sheetSide*longest, openAIImagesGIFSheetMaxEdge,
		maxWidth, maxHeight, frameCount, maxFramesAtSize, frameWord, size,
	)
}

// openAIGIFAnimationControls are the GIF animation fields a caller explicitly
// set on /v1/images/{generations,edits}. Nil pointers mean "not requested".
type openAIGIFAnimationControls struct {
	frameCount      *int
	frameDurationMS *int
	size            string
}

func openAIGIFAnimationControlsFromRequest(parsed *OpenAIImagesRequest) openAIGIFAnimationControls {
	if parsed == nil {
		return openAIGIFAnimationControls{}
	}
	return openAIGIFAnimationControls{
		frameCount:      parsed.FrameCount,
		frameDurationMS: parsed.FrameDurationMS,
		size:            parsed.Size,
	}
}

func (c openAIGIFAnimationControls) empty() bool {
	return c.frameCount == nil && c.frameDurationMS == nil && strings.TrimSpace(c.size) == ""
}

// normalizeOpenAIImagesGIFAnimationResponseBody rewrites every GIF result in an
// Images API JSON response so explicitly requested animation controls take
// effect even when the upstream normalized them to its own defaults (for the
// Codex OAuth native Images endpoint: 9 frames, 200 ms per frame and a ~2K
// longest edge). Non-GIF results and responses that already match are returned
// untouched, so the rewrite only ever repairs a mismatch.
func normalizeOpenAIImagesGIFAnimationResponseBody(body []byte, parsed *OpenAIImagesRequest) []byte {
	controls := openAIGIFAnimationControlsFromRequest(parsed)
	if controls.empty() || len(body) == 0 || !gjson.ValidBytes(body) {
		return body
	}
	items := gjson.GetBytes(body, "data")
	if !items.IsArray() {
		return body
	}
	defaultFormat := strings.TrimSpace(gjson.GetBytes(body, "output_format").String())
	if defaultFormat == "" {
		defaultFormat = strings.TrimSpace(parsed.OutputFormat)
	}
	for index, item := range items.Array() {
		encoded := strings.TrimSpace(item.Get("b64_json").String())
		if encoded == "" {
			continue
		}
		format := strings.TrimSpace(item.Get("output_format").String())
		if format == "" {
			format = defaultFormat
		}
		if !strings.EqualFold(format, "gif") {
			continue
		}
		normalized, changed := normalizeOpenAIImagesGIFBase64(encoded, controls)
		if !changed {
			continue
		}
		updated, err := sjson.SetBytes(body, fmt.Sprintf("data.%d.b64_json", index), normalized)
		if err != nil {
			continue
		}
		body = updated
	}
	return body
}

// normalizeOpenAIImagesGIFAnimationEvent rewrites a single GIF stream event
// (image_generation.completed / image_edit.completed) built by the native Images
// endpoint. It is the streaming counterpart of
// normalizeOpenAIImagesGIFAnimationResponseBody.
func normalizeOpenAIImagesGIFAnimationEvent(data []byte, parsed *OpenAIImagesRequest) []byte {
	controls := openAIGIFAnimationControlsFromRequest(parsed)
	if controls.empty() || len(data) == 0 || !gjson.ValidBytes(data) {
		return data
	}
	encoded := strings.TrimSpace(gjson.GetBytes(data, "b64_json").String())
	if encoded == "" {
		return data
	}
	format := strings.TrimSpace(gjson.GetBytes(data, "output_format").String())
	if format == "" {
		format = strings.TrimSpace(parsed.OutputFormat)
	}
	if !strings.EqualFold(format, "gif") {
		return data
	}
	normalized, changed := normalizeOpenAIImagesGIFBase64(encoded, controls)
	if !changed {
		return data
	}
	updated, err := sjson.SetBytes(data, "b64_json", normalized)
	if err != nil {
		return data
	}
	return updated
}

func normalizeOpenAIImagesGIFBase64(encoded string, controls openAIGIFAnimationControls) (string, bool) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil {
		return encoded, false
	}
	normalized := normalizeOpenAIImagesGIFAnimation(raw, controls)
	if bytes.Equal(normalized, raw) {
		return encoded, false
	}
	return base64.StdEncoding.EncodeToString(normalized), true
}

// normalizeOpenAIImagesGIFAnimation returns GIF bytes that honor the caller's
// explicit frame_count, frame_duration_ms and per-frame size controls. The input
// bytes are returned unchanged when no adjustment is needed or possible (not a
// GIF, an absurdly large animation, or upstream bytes that already match), which
// keeps the no-op path cheap and lossless.
func normalizeOpenAIImagesGIFAnimation(data []byte, controls openAIGIFAnimationControls) []byte {
	if controls.empty() || len(data) == 0 {
		return data
	}
	// Probe the block structure first: it is cheap, and it lets the no-op path
	// and the work guard run without decoding a single frame.
	metadata, ok := scanOpenAIGIFMetadata(data)
	if !ok || metadata.frameCount == 0 {
		return data
	}
	if int64(metadata.frameCount)*int64(metadata.width)*int64(metadata.height) > maxOpenAIGIFAnimationPixels {
		return data
	}

	frameCount, frameCountRequested := openAIGIFTargetFrameCount(controls.frameCount)
	if !frameCountRequested {
		frameCount = metadata.frameCount
	}
	frameDurationCS, frameDurationRequested := openAIGIFTargetFrameDurationCS(controls.frameDurationMS)
	sizeWidth, sizeHeight, sizeRequested := openAIGIFTargetFrameSize(controls.size)
	if !sizeRequested {
		sizeWidth, sizeHeight = metadata.width, metadata.height
	}

	countChanged := frameCount != metadata.frameCount
	sizeChanged := sizeWidth != metadata.width || sizeHeight != metadata.height
	delayChanged := false
	if frameDurationRequested {
		delayChanged = frameCount != metadata.frameCount
		for _, delay := range metadata.delays {
			if delay != frameDurationCS {
				delayChanged = true
				break
			}
		}
	}
	if !countChanged && !sizeChanged && !delayChanged {
		// The upstream already honored every explicit control.
		return data
	}
	if int64(frameCount)*int64(sizeWidth)*int64(sizeHeight) > maxOpenAIGIFAnimationPixels {
		// Refuse to build an animation larger than the guard, but keep the
		// upstream output rather than failing the request that already ran.
		return data
	}

	animation, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil || len(animation.Image) == 0 {
		return data
	}
	srcCount := len(animation.Image)
	width, height := metadata.width, metadata.height

	delays := make([]int, frameCount)
	for index := 0; index < frameCount; index++ {
		sourceIndex := openAIGIFFrameIndex(index, srcCount, frameCount)
		switch {
		case frameDurationRequested:
			delays[index] = frameDurationCS
		case sourceIndex < len(animation.Delay):
			delays[index] = animation.Delay[sourceIndex]
		}
	}

	if frameCount < srcCount && openAIGIFReductionNeedsComposition(animation, width, height) {
		// Delta frames only carry the region that changed since the previous
		// frame, so dropping frames and re-encoding the raw sub-rectangles leaves
		// the dropped frames' pixels on the canvas. Composite the animation the
		// way a viewer renders it, then sample the requested frames.
		images, palette, ok := sampleComposedOpenAIGIFFrames(animation, width, height, sizeWidth, sizeHeight, sizeChanged, frameCount)
		if !ok {
			// Keep the upstream animation rather than returning a corrupted one.
			return data
		}
		return encodeOpenAIGIFAnimation(&gif.GIF{
			Image:           images,
			Delay:           delays,
			LoopCount:       animation.LoopCount,
			Disposal:        make([]byte, frameCount),
			Config:          image.Config{ColorModel: palette, Width: sizeWidth, Height: sizeHeight},
			BackgroundIndex: 0,
		}, data)
	}

	resizedFrames := make(map[int]*image.Paletted, srcCount)
	images := make([]*image.Paletted, frameCount)
	disposals := make([]byte, frameCount)
	for index := 0; index < frameCount; index++ {
		sourceIndex := openAIGIFFrameIndex(index, srcCount, frameCount)
		source := animation.Image[sourceIndex]
		if sizeChanged {
			if cached, ok := resizedFrames[sourceIndex]; ok {
				images[index] = cached
			} else {
				images[index] = resizeOpenAIImagesGIFFrame(source, width, height, sizeWidth, sizeHeight)
				resizedFrames[sourceIndex] = images[index]
			}
		} else {
			images[index] = source
		}
		if sourceIndex < len(animation.Disposal) {
			disposals[index] = animation.Disposal[sourceIndex]
		}
	}

	return encodeOpenAIGIFAnimation(&gif.GIF{
		Image:           images,
		Delay:           delays,
		LoopCount:       animation.LoopCount,
		Disposal:        disposals,
		Config:          image.Config{ColorModel: animation.Config.ColorModel, Width: sizeWidth, Height: sizeHeight},
		BackgroundIndex: animation.BackgroundIndex,
	}, data)
}

// openAIGIFReductionNeedsComposition reports whether sampling source frames
// directly can show the wrong picture: a frame that only covers part of the
// canvas, or a frame with transparent pixels, depends on the frames before it
// once a frame is dropped.
func openAIGIFReductionNeedsComposition(animation *gif.GIF, width, height int) bool {
	canvasRect := image.Rect(0, 0, width, height)
	for _, frame := range animation.Image {
		if frame.Bounds() != canvasRect {
			return true
		}
		for _, entry := range frame.Palette {
			if _, _, _, alpha := entry.RGBA(); uint8(alpha>>8) == 0 {
				return true
			}
		}
	}
	return false
}

// openAIGIFFrameIndex maps an output frame to the source frame it samples.
func openAIGIFFrameIndex(index, sourceCount, targetCount int) int {
	if targetCount <= 0 || sourceCount <= 0 {
		return 0
	}
	sourceIndex := index * sourceCount / targetCount
	if sourceIndex >= sourceCount {
		sourceIndex = sourceCount - 1
	}
	return sourceIndex
}

// encodeOpenAIGIFAnimation encodes the rewritten animation, falling back to the
// untouched upstream bytes when encoding fails.
func encodeOpenAIGIFAnimation(animation *gif.GIF, upstream []byte) []byte {
	var buffer bytes.Buffer
	if err := gif.EncodeAll(&buffer, animation); err != nil {
		return upstream
	}
	return buffer.Bytes()
}

// sampleComposedOpenAIGIFFrames composites the source animation and returns the
// requested full-canvas frames together with the single palette they share.
func sampleComposedOpenAIGIFFrames(animation *gif.GIF, width, height, sizeWidth, sizeHeight int, resize bool, frameCount int) ([]*image.Paletted, color.Palette, bool) {
	srcCount := len(animation.Image)
	keep := make(map[int]bool, frameCount)
	for index := 0; index < frameCount; index++ {
		keep[openAIGIFFrameIndex(index, srcCount, frameCount)] = true
	}
	composed, palette, ok := composeOpenAIGIFFrames(animation, width, height, keep)
	if !ok {
		return nil, nil, false
	}
	images := make([]*image.Paletted, frameCount)
	resized := make(map[int]*image.Paletted, len(composed))
	for index := 0; index < frameCount; index++ {
		sourceIndex := openAIGIFFrameIndex(index, srcCount, frameCount)
		if !resize {
			images[index] = composed[sourceIndex]
			continue
		}
		if cached, ok := resized[sourceIndex]; ok {
			images[index] = cached
			continue
		}
		images[index] = resizeOpenAIImagesGIFFrame(composed[sourceIndex], width, height, sizeWidth, sizeHeight)
		resized[sourceIndex] = images[index]
	}
	return images, palette, true
}

// maxOpenAIGIFComposePixels bounds the RGBA canvas a delta-frame animation may
// allocate while it is composited. Larger animations keep their upstream bytes
// instead of spending hundreds of megabytes on one response.
const maxOpenAIGIFComposePixels = 8 << 20

// composeOpenAIGIFFrames renders the animation the way a viewer displays it: it
// applies sub-rectangle delta frames, transparency and Disposal onto a full
// canvas and snapshots the requested frames. Delta frames depend on the frames
// before them, so sampling them directly would keep the dropped frames' pixels
// on the canvas.
func composeOpenAIGIFFrames(animation *gif.GIF, width, height int, keep map[int]bool) (map[int]*image.Paletted, color.Palette, bool) {
	if width <= 0 || height <= 0 || int64(width)*int64(height) > maxOpenAIGIFComposePixels {
		return nil, nil, false
	}
	palette, ok := composeOpenAIGIFPalette(animation.Image)
	if !ok {
		return nil, nil, false
	}
	transparentIndex := -1
	lookup := make(map[color.RGBA]uint8, len(palette))
	for index, entry := range palette {
		r, g, b, a := entry.RGBA()
		if uint8(a>>8) == 0 {
			transparentIndex = index
			continue
		}
		lookup[color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xff}] = uint8(index)
	}

	canvasRect := image.Rect(0, 0, width, height)
	canvas := image.NewRGBA(canvasRect)
	frames := make(map[int]*image.Paletted, len(keep))
	var previous *image.RGBA
	for index, source := range animation.Image {
		disposal := byte(0)
		if index < len(animation.Disposal) {
			disposal = animation.Disposal[index]
		}
		if disposal == gif.DisposalPrevious {
			previous = composeOpenAIGIFCloneRGBA(canvas)
		}
		draw.Draw(canvas, source.Bounds().Intersect(canvasRect), source, source.Bounds().Min, draw.Over)
		if keep[index] {
			frame, ok := composeOpenAIGIFPalettedFrame(canvas, palette, lookup, transparentIndex)
			if !ok {
				return nil, nil, false
			}
			frames[index] = frame
		}
		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvas, source.Bounds().Intersect(canvasRect), image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			if previous != nil {
				copy(canvas.Pix, previous.Pix)
			}
		}
	}
	return frames, palette, true
}

// composeOpenAIGIFPalette collects the opaque colors of every source frame and
// reserves one transparent entry, since rendered frames may leave pixels
// unpainted and GIF has no other way to express transparency.
func composeOpenAIGIFPalette(images []*image.Paletted) (color.Palette, bool) {
	palette := make(color.Palette, 0, 256)
	seen := make(map[color.RGBA]bool, 256)
	for _, frame := range images {
		for _, entry := range frame.Palette {
			r, g, b, a := entry.RGBA()
			if uint8(a>>8) == 0 {
				continue
			}
			key := color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xff}
			if seen[key] {
				continue
			}
			seen[key] = true
			palette = append(palette, key)
		}
	}
	if len(palette) == 0 || len(palette) >= 256 {
		return nil, false
	}
	return append(palette, color.RGBA{}), true
}

// composeOpenAIGIFPalettedFrame converts one displayed RGBA canvas to the shared
// palette. Colors must exist in the palette; a blended or out-of-palette color
// means the animation cannot be rewritten losslessly, so it is left untouched.
func composeOpenAIGIFPalettedFrame(canvas *image.RGBA, palette color.Palette, lookup map[color.RGBA]uint8, transparentIndex int) (*image.Paletted, bool) {
	bounds := canvas.Bounds()
	frame := image.NewPaletted(bounds, palette)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			offset := canvas.PixOffset(x, y)
			if canvas.Pix[offset+3] == 0 {
				if transparentIndex < 0 {
					return nil, false
				}
				frame.Pix[frame.PixOffset(x, y)] = uint8(transparentIndex)
				continue
			}
			key := color.RGBA{R: canvas.Pix[offset], G: canvas.Pix[offset+1], B: canvas.Pix[offset+2], A: 0xff}
			index, ok := lookup[key]
			if !ok {
				return nil, false
			}
			frame.Pix[frame.PixOffset(x, y)] = index
		}
	}
	return frame, true
}

func composeOpenAIGIFCloneRGBA(source *image.RGBA) *image.RGBA {
	clone := image.NewRGBA(source.Bounds())
	copy(clone.Pix, source.Pix)
	return clone
}

// openAIGIFMetadata is the block-structure summary of an animation: enough to
// decide whether a requested animation control is already satisfied without
// decoding any pixel data.
type openAIGIFMetadata struct {
	width      int
	height     int
	frameCount int
	delays     []int // per frame, in 1/100 s
}

// scanOpenAIGIFMetadata walks the GIF block structure instead of decoding it.
// Only the logical screen, the graphic control delays and the frame rectangles
// are read, so probing a large animation never allocates frame buffers.
func scanOpenAIGIFMetadata(data []byte) (openAIGIFMetadata, bool) {
	var metadata openAIGIFMetadata
	if len(data) < 13 {
		return metadata, false
	}
	header := string(data[:6])
	if header != "GIF87a" && header != "GIF89a" {
		return metadata, false
	}
	metadata.width = int(binary.LittleEndian.Uint16(data[6:8]))
	metadata.height = int(binary.LittleEndian.Uint16(data[8:10]))
	if metadata.width <= 0 || metadata.height <= 0 {
		return metadata, false
	}
	position := 13
	if data[10]&0x80 != 0 {
		position += 3 << ((data[10] & 0x07) + 1)
	}
	pendingDelay := 0
	for position < len(data) {
		switch data[position] {
		case 0x3b: // trailer
			return metadata, metadata.frameCount > 0
		case 0x21: // extension introducer
			if position+1 >= len(data) {
				return metadata, false
			}
			label := data[position+1]
			position += 2
			if label == 0xf9 {
				if position >= len(data) || int(data[position]) != 4 || position+4 >= len(data) {
					return metadata, false
				}
				pendingDelay = int(binary.LittleEndian.Uint16(data[position+2 : position+4]))
			}
			var ok bool
			position, ok = skipOpenAIGIFSubBlocks(data, position)
			if !ok {
				return metadata, false
			}
		case 0x2c: // image descriptor
			if position+10 > len(data) {
				return metadata, false
			}
			fields := data[position+9]
			position += 10
			if fields&0x80 != 0 {
				position += 3 << ((fields & 0x07) + 1)
			}
			if position >= len(data) {
				return metadata, false
			}
			position++ // LZW minimum code size
			var ok bool
			position, ok = skipOpenAIGIFSubBlocks(data, position)
			if !ok {
				return metadata, false
			}
			metadata.frameCount++
			metadata.delays = append(metadata.delays, pendingDelay)
			pendingDelay = 0
		default:
			return metadata, false
		}
	}
	return metadata, false
}

// skipOpenAIGIFSubBlocks advances past one sub-block chain, including its
// zero-length terminator.
func skipOpenAIGIFSubBlocks(data []byte, position int) (int, bool) {
	for position < len(data) {
		size := int(data[position])
		position++
		if size == 0 {
			return position, true
		}
		if position+size > len(data) {
			return 0, false
		}
		position += size
	}
	return 0, false
}

// openAIGIFTargetFrameCount reports the requested frame count and whether it is
// within the documented range. Out-of-range values stay upstream-owned.
func openAIGIFTargetFrameCount(requested *int) (int, bool) {
	if requested == nil {
		return 0, false
	}
	value := *requested
	if value < openAIGIFMinFrameCount || value > openAIGIFMaxFrameCount {
		return 0, false
	}
	return value, true
}

// openAIGIFTargetFrameDurationCS converts a requested per-frame duration to GIF
// centiseconds. The documented controls are 20-2000 ms rounded to 10 ms, which
// is exactly the GIF delay unit.
func openAIGIFTargetFrameDurationCS(requested *int) (int, bool) {
	if requested == nil {
		return 0, false
	}
	ms := *requested
	if ms < openAIGIFMinFrameDurationMS || ms > openAIGIFMaxFrameDurationMS {
		return 0, false
	}
	centiseconds := int(math.Round(float64(ms) / float64(openAIGIFDurationRoundToMS)))
	if centiseconds < openAIGIFMinFrameDurationMS/openAIGIFDurationRoundToMS {
		centiseconds = openAIGIFMinFrameDurationMS / openAIGIFDurationRoundToMS
	}
	return centiseconds, true
}

// openAIGIFTargetFrameSize parses the requested per-frame size (WxH). "auto" and
// other non-pixel values keep the upstream size.
func openAIGIFTargetFrameSize(size string) (int, int, bool) {
	widthText, heightText, ok := strings.Cut(strings.ToLower(strings.TrimSpace(size)), "x")
	if !ok {
		return 0, 0, false
	}
	width, widthErr := strconv.Atoi(strings.TrimSpace(widthText))
	height, heightErr := strconv.Atoi(strings.TrimSpace(heightText))
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

// resizeOpenAIImagesGIFFrame scales one frame while preserving its palette and
// its sub-rectangle offset, so animations made of partial frames keep their
// disposal-based composition. The catalogue of decoded frames keeps the mapping
// between the source canvas and the requested per-frame size.
func resizeOpenAIImagesGIFFrame(source *image.Paletted, canvasWidth, canvasHeight, width, height int) *image.Paletted {
	sourceBounds := source.Bounds()
	if sourceBounds.Empty() || canvasWidth <= 0 || canvasHeight <= 0 {
		return source
	}
	scaleX := float64(width) / float64(canvasWidth)
	scaleY := float64(height) / float64(canvasHeight)
	left := clampOpenAIImageCoordinate(scaleOpenAIImageCoordinate(sourceBounds.Min.X, scaleX), width-1)
	top := clampOpenAIImageCoordinate(scaleOpenAIImageCoordinate(sourceBounds.Min.Y, scaleY), height-1)
	right := clampOpenAIImageCoordinate(scaleOpenAIImageCoordinate(sourceBounds.Max.X, scaleX), width)
	bottom := clampOpenAIImageCoordinate(scaleOpenAIImageCoordinate(sourceBounds.Max.Y, scaleY), height)
	targetBounds := image.Rect(left, top, max(right, left+1), max(bottom, top+1))
	target := image.NewPaletted(targetBounds, source.Palette)
	if targetBounds.Dx() == sourceBounds.Dx() && targetBounds.Dy() == sourceBounds.Dy() {
		copy(target.Pix, source.Pix)
		return target
	}
	xdraw.CatmullRom.Scale(target, targetBounds, source, sourceBounds, draw.Src, nil)
	return target
}

func scaleOpenAIImageCoordinate(value int, scale float64) int {
	return int(math.Round(float64(value) * scale))
}

func clampOpenAIImageCoordinate(value, canvasLength int) int {
	if value < 0 {
		return 0
	}
	if value > canvasLength {
		return canvasLength
	}
	return value
}
