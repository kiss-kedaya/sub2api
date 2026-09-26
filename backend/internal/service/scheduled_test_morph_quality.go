package service

import (
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/css"
	"golang.org/x/net/html"
)

type qualityMorphPoint struct{ x, y float64 }
type qualityMorphSegment struct{ a, b qualityMorphPoint }
type qualityMorphPath struct {
	start, end qualityMorphPoint
	points     []qualityMorphPoint // Bezier control hull contains the entire curve.
	lines      []qualityMorphSegment
	signature  string
	closed     bool
	moves      int
}
type qualityMorphFrame struct {
	offset float64
	values map[string]string
}
type qualityMorphSheet struct {
	css    qualityCSS
	frames map[string][]qualityMorphFrame
}
type qualityMorphTrack struct {
	node     *html.Node
	paths    []qualityMorphPath
	duration float64
	css      bool
}
type qualityMorphRotor struct {
	node      *html.Node
	center    qualityMorphPoint
	tips      []qualityMorphPoint
	minRadius float64
	stroke    float64
	duration  float64
}

// The caller owns the no-script precondition and cycling-task context. Empty
// means inconclusive, never a pass. Only ordinary (non-reduced-motion) playback
// is assessed. No class/id vocabulary is used to infer limbs or pedals.
func qualityMorphFailure(doc *html.Node) string {
	if doc == nil {
		return ""
	}
	var styles strings.Builder
	var nodes []*html.Node
	valid := true
	qualityWalk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		nodes = append(nodes, n)
		if n.Data == "link" || n.Data == "use" || n.Data == "script" {
			valid = false
		}
		for _, a := range n.Attr {
			if strings.HasPrefix(strings.ToLower(a.Key), "on") {
				valid = false
			}
		}
		if n.Data == "style" {
			if qualityAttr(n, "media") != "" {
				valid = false
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.TextNode {
					styles.WriteString(c.Data)
					styles.WriteByte('\n')
				}
			}
		}
		if (n.Data == "animate" || n.Data == "animateTransform" || n.Data == "set" || n.Data == "animateMotion") && qualityAttr(n, "href") != "" {
			valid = false
		}
	})
	if !valid || len(nodes) > 5000 || styles.Len() > 256*1024 {
		return ""
	}
	sheet, ok := qualityMorphParseCSS(styles.String())
	if !ok {
		return ""
	}
	var tracks []qualityMorphTrack
	var rotors []qualityMorphRotor
	for _, n := range nodes {
		if n.Namespace != "svg" {
			continue
		}
		if n.Data == "path" {
			if track, ok := qualityMorphReadTrack(n, sheet); ok {
				tracks = append(tracks, track)
			}
		}
		if n.Data == "g" {
			if rotor, ok := qualityMorphReadRotor(n, sheet); ok {
				rotors = append(rotors, rotor)
			}
		}
	}
	if len(tracks) > 64 || len(rotors) > 64 {
		return ""
	}
	for _, rotor := range rotors {
		for _, track := range tracks {
			if !track.css && qualityMorphChordMismatch(track, rotor, sheet) {
				return "quality check failed: SMIL path endpoint crosses inside the rotating pedal orbit instead of staying attached"
			}
		}
		if qualityMorphDetachedFeet(tracks, rotor, sheet) {
			return "quality check failed: CSS path-morphed endpoints leave both stationary foot shapes during the pedal cycle"
		}
	}
	return ""
}

func qualityMorphRelevant(key string) bool {
	return qualityMotionProperty(key) || key == "d" || key == "display" || key == "visibility" || key == "opacity" || key == "fill" || key == "stroke" || key == "stroke-width" || key == "filter" || key == "clip-path" || key == "mask" || key == "vector-effect" || key == "cx" || key == "cy" || key == "r" || strings.HasPrefix(key, "transition") || strings.HasPrefix(key, "marker-")
}

func qualityMorphParseCSS(text string) (qualityMorphSheet, bool) {
	sheet := qualityMorphSheet{frames: make(map[string][]qualityMorphFrame)}
	type block struct {
		name            string
		offsets         []float64
		selector        string
		ignore, unknown bool
	}
	var stack []block
	selectors := ""
	p := css.NewParser(parse.NewInputString(text), false)
	for {
		kind, _, data := p.Next()
		value := qualityCSSValues(p)
		switch kind {
		case css.ErrorGrammar:
			if p.Err() != io.EOF || len(stack) != 0 {
				return sheet, false
			}
			for name, frames := range sheet.frames {
				sort.Slice(frames, func(i, j int) bool { return frames[i].offset < frames[j].offset })
				for i := 1; i < len(frames); i++ {
					if frames[i].offset == frames[i-1].offset {
						return sheet, false
					}
				}
				sheet.frames[name] = frames
			}
			return sheet, true
		case css.AtRuleGrammar:
			return sheet, false
		case css.QualifiedRuleGrammar:
			selectors += value + ","
		case css.BeginAtRuleGrammar:
			b := block{unknown: true}
			if len(stack) == 0 && string(data) == "@keyframes" {
				if _, exists := sheet.frames[value]; exists {
					return sheet, false
				}
				sheet.frames[value] = nil
				b = block{name: value}
			} else if len(stack) == 0 && string(data) == "@media" && strings.Join(strings.Fields(value), "") == "(prefers-reduced-motion:reduce)" {
				b = block{ignore: true}
			} else if len(stack) > 0 && stack[len(stack)-1].ignore {
				b.ignore = true
			}
			stack = append(stack, b)
		case css.BeginRulesetGrammar:
			b := block{selector: selectors + value}
			selectors = ""
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				b.name, b.ignore, b.unknown = parent.name, parent.ignore, parent.unknown
			}
			if b.name != "" {
				for _, selector := range strings.Split(b.selector, ",") {
					selector = strings.TrimSpace(selector)
					switch selector {
					case "from":
						selector = "0%"
					case "to":
						selector = "100%"
					}
					if !strings.HasSuffix(selector, "%") {
						return sheet, false
					}
					offset, ok := qualityMorphNumber(strings.TrimSuffix(selector, "%"))
					if !ok || offset < 0 || offset > 100 {
						return sheet, false
					}
					b.offsets = append(b.offsets, offset/100)
					sheet.frames[b.name] = append(sheet.frames[b.name], qualityMorphFrame{offset / 100, make(map[string]string)})
				}
			}
			stack = append(stack, b)
		case css.EndRulesetGrammar, css.EndAtRuleGrammar:
			if len(stack) == 0 {
				return sheet, false
			}
			stack = stack[:len(stack)-1]
		case css.DeclarationGrammar:
			key := string(data)
			if !qualityMorphRelevant(key) {
				continue
			}
			if len(stack) == 0 {
				return sheet, false
			}
			b := stack[len(stack)-1]
			if b.ignore {
				continue
			}
			if b.unknown {
				return sheet, false
			}
			if b.name != "" {
				frames := sheet.frames[b.name]
				for i := len(frames) - len(b.offsets); i < len(frames); i++ {
					frames[i].values[key] = value
				}
				continue
			}
			for _, selector := range strings.Split(b.selector, ",") {
				selector = strings.TrimSpace(selector)
				if !qualitySimpleSelector.MatchString(selector) {
					return sheet, false
				}
				sheet.css.rules = append(sheet.css.rules, qualityCSSRule{selector: selector, property: key, value: value})
			}
		}
	}
}

func qualityMorphNumber(value string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return v, err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) < 1e7
}
func qualityMorphDuration(value string) (float64, bool) {
	scale := 1.0
	if strings.HasSuffix(value, "ms") {
		scale = .001
		value = strings.TrimSuffix(value, "ms")
	} else if strings.HasSuffix(value, "s") {
		value = strings.TrimSuffix(value, "s")
	} else {
		return 0, false
	}
	v, ok := qualityMorphNumber(value)
	return v * scale, ok && v*scale > 0
}

// Deliberately excludes delays, steps, longhand overrides and composition.
func (sheet qualityMorphSheet) animation(n *html.Node) ([]qualityMorphFrame, float64, bool) {
	style, ok := sheet.css.style(n)
	if !ok {
		return nil, 0, false
	}
	for k := range style {
		if strings.HasPrefix(k, "animation-") {
			return nil, 0, false
		}
	}
	var frames []qualityMorphFrame
	var duration float64
	infinite, timing := false, false
	for _, field := range strings.Fields(style["animation"]) {
		if f, exists := sheet.frames[field]; exists && frames == nil {
			frames = f
			continue
		}
		if d, valid := qualityMorphDuration(field); valid && duration == 0 {
			duration = d
			continue
		}
		if field == "infinite" && !infinite {
			infinite = true
			continue
		}
		if (field == "linear" || field == "ease" || field == "ease-in" || field == "ease-out" || field == "ease-in-out") && !timing {
			timing = true
			continue
		}
		if field == "normal" || field == "alternate" || field == "reverse" || field == "alternate-reverse" || field == "running" {
			continue
		}
		return nil, 0, false
	}
	return frames, duration, len(frames) > 0 && duration > 0 && infinite
}

func qualityMorphParsePath(text string) (qualityMorphPath, bool) {
	var out qualityMorphPath
	if len(text) > 8192 {
		return out, false
	}
	var command byte
	var current, start qualityMorphPoint
	var signature strings.Builder
	for {
		text = strings.TrimLeft(text, " ,\t\r\n")
		if text == "" {
			break
		}
		explicit := strings.ContainsRune("MmLlHhVvQqCcZz", rune(text[0]))
		if explicit {
			command = text[0]
			text = text[1:]
		}
		if command == 0 || (out.moves == 0 && command != 'M' && command != 'm') {
			return out, false
		}
		if command == 'Z' || command == 'z' {
			if !explicit {
				return out, false
			}
			out.closed = true
			current = start
			signature.WriteByte(command)
			command = 0
			continue
		}
		width := 2
		switch command {
		case 'H', 'h', 'V', 'v':
			width = 1
		case 'Q', 'q':
			width = 4
		case 'C', 'c':
			width = 6
		}
		v := make([]float64, width)
		for i := range v {
			text = strings.TrimLeft(text, " ,\t\r\n")
			n := parse.Number([]byte(text))
			if n == 0 {
				return out, false
			}
			var ok bool
			v[i], ok = qualityMorphNumber(text[:n])
			if !ok {
				return out, false
			}
			text = text[n:]
		}
		signature.WriteByte(command)
		relative := command >= 'a' && command <= 'z'
		points := make([]qualityMorphPoint, 0, 3)
		if command == 'H' || command == 'h' {
			x := v[0]
			if relative {
				x += current.x
			}
			points = append(points, qualityMorphPoint{x, current.y})
		} else if command == 'V' || command == 'v' {
			y := v[0]
			if relative {
				y += current.y
			}
			points = append(points, qualityMorphPoint{current.x, y})
		} else {
			for i := 0; i < len(v); i += 2 {
				p := qualityMorphPoint{v[i], v[i+1]}
				if relative {
					p.x += current.x
					p.y += current.y
				}
				points = append(points, p)
			}
		}
		end := points[len(points)-1]
		if command == 'M' || command == 'm' {
			out.moves++
			start = end
			if out.moves == 1 {
				out.start = end
			}
			if command == 'M' {
				command = 'L'
			} else {
				command = 'l'
			}
		} else if len(points) == 1 {
			out.lines = append(out.lines, qualityMorphSegment{current, end})
		}
		out.points = append(out.points, points...)
		current = end
		if len(out.points) > 256 {
			return out, false
		}
	}
	out.end = current
	out.signature = signature.String()
	return out, out.moves > 0 && len(out.points) >= 2
}
func qualityMorphPathValue(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "path(") || !strings.HasSuffix(value, ")") {
		return ""
	}
	value = strings.TrimSpace(value[5 : len(value)-1])
	if len(value) < 2 || (value[0] != '\'' && value[0] != '"') || value[len(value)-1] != value[0] || strings.Contains(value, "\\") {
		return ""
	}
	return value[1 : len(value)-1]
}
func qualityMorphDistance(a, b qualityMorphPoint) float64 { return math.Hypot(a.x-b.x, a.y-b.y) }
func qualityMorphSame(a, b float64) bool                  { return math.Abs(a-b) < 1e-7 }
func qualityMorphHasAnimation(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (strings.HasPrefix(c.Data, "animate") || c.Data == "set") {
			return true
		}
	}
	return false
}

func (sheet qualityMorphSheet) property(n *html.Node, key, fallback string, inherit bool) string {
	for ; n != nil && n.Type != html.DocumentNode; n = n.Parent {
		style, ok := sheet.css.style(n)
		if !ok {
			return "?"
		}
		if value, exists := style[key]; exists {
			return value
		}
		if value := qualityAttr(n, key); value != "" {
			return value
		}
		if !inherit {
			break
		}
	}
	return fallback
}

func (sheet qualityMorphSheet) visible(n *html.Node) bool {
	for ; n != nil; n = n.Parent {
		if n.Type != html.ElementNode {
			continue
		}
		switch n.Data {
		case "defs", "symbol", "clipPath", "mask", "marker", "pattern", "switch":
			return false
		}
		style, ok := sheet.css.style(n)
		if !ok {
			return false
		}
		for _, key := range []string{"display", "visibility", "opacity", "filter", "clip-path", "mask", "vector-effect"} {
			v := style[key]
			if v == "" {
				v = qualityAttr(n, key)
			}
			if v == "" {
				continue
			}
			switch key {
			case "display":
				if v == "none" || strings.Contains(v, "var(") || strings.Contains(v, "inherit") {
					return false
				}
			case "visibility":
				if v != "visible" {
					return false
				}
			case "opacity":
				if number, ok := qualityMorphNumber(v); !ok || number <= 0 {
					return false
				}
			default:
				if v != "none" {
					return false
				}
			}
		}
		for key := range style {
			if strings.HasPrefix(key, "transition") || strings.HasPrefix(key, "marker-") {
				return false
			}
		}
		for _, a := range n.Attr {
			if strings.HasPrefix(a.Key, "marker-") || a.Key == "requiredFeatures" || a.Key == "systemLanguage" {
				return false
			}
		}
	}
	return true
}

func (sheet qualityMorphSheet) stroke(n *html.Node) (float64, bool) {
	paint := sheet.property(n, "stroke", "none", true)
	if paint == "none" || paint == "transparent" || strings.Contains(paint, "var(") || paint == "?" {
		return 0, false
	}
	v, ok := qualityMorphNumber(sheet.property(n, "stroke-width", "1", true))
	return v, ok && v > 0
}

func qualityMorphReadTrack(n *html.Node, sheet qualityMorphSheet) (qualityMorphTrack, bool) {
	track := qualityMorphTrack{node: n}
	if !sheet.visible(n) {
		return track, false
	}
	if _, ok := sheet.stroke(n); !ok {
		return track, false
	}
	if sheet.property(n, "fill", "black", true) != "none" || qualityAttr(n, "transform") != "" {
		return track, false
	}
	style, ok := sheet.css.style(n)
	if !ok || style["transform"] != "" {
		return track, false
	}
	var values []string
	if style["animation"] != "" {
		if qualityMorphHasAnimation(n) {
			return track, false
		}
		frames, duration, ok := sheet.animation(n)
		if !ok || len(frames) != 3 || frames[0].offset != 0 || frames[1].offset != .5 || frames[2].offset != 1 {
			return track, false
		}
		for _, frame := range frames {
			if len(frame.values) != 1 {
				return track, false
			}
			values = append(values, qualityMorphPathValue(frame.values["d"]))
		}
		track.duration = duration
		track.css = true
	} else {
		if style["d"] != "" {
			return track, false
		}
		for key := range style {
			if strings.HasPrefix(key, "animation-") {
				return track, false
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			if c.Data != "animate" || len(values) > 0 || qualityAttr(c, "attributeName") != "d" || !qualityMorphSMILSimple(c) {
				return track, false
			}
			track.duration, ok = qualityMorphDuration(qualityAttr(c, "dur"))
			if !ok {
				return track, false
			}
			values = strings.Split(qualityAttr(c, "values"), ";")
		}
	}
	if len(values) < 3 || len(values) > 32 {
		return track, false
	}
	for _, value := range values {
		path, ok := qualityMorphParsePath(value)
		if !ok || path.closed || path.moves != 1 {
			return track, false
		}
		if len(track.paths) > 0 && (path.signature != track.paths[0].signature || qualityMorphDistance(path.start, track.paths[0].start) > .001) {
			return track, false
		}
		track.paths = append(track.paths, path)
	}
	if qualityMorphDistance(track.paths[0].end, track.paths[len(track.paths)-1].end) > .001 {
		return track, false
	}
	return track, true
}

func qualityMorphSMILSimple(n *html.Node) bool {
	if qualityAttr(n, "repeatCount") != "indefinite" {
		return false
	}
	for _, a := range n.Attr {
		switch a.Key {
		case "attributeName", "dur", "repeatCount", "values", "type", "from", "to", "id":
		case "calcMode":
			if a.Val != "linear" {
				return false
			}
		case "begin":
			if a.Val != "0s" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func qualityMorphReadRotor(n *html.Node, sheet qualityMorphSheet) (qualityMorphRotor, bool) {
	r := qualityMorphRotor{node: n}
	if !sheet.visible(n) || qualityAttr(n, "transform") != "" {
		return r, false
	}
	style, ok := sheet.css.style(n)
	if !ok || style["transform"] != "" {
		return r, false
	}
	var hub qualityMorphPoint
	var points []qualityMorphPoint
	var lines []qualityMorphSegment
	circles := 0
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if c.Data == "animateTransform" {
			continue
		}
		if !sheet.visible(c) || qualityAttr(c, "transform") != "" || qualityMorphHasAnimation(c) {
			return r, false
		}
		cs, ok := sheet.css.style(c)
		if !ok {
			return r, false
		}
		for k := range cs {
			if qualityMotionProperty(k) || k == "d" || k == "cx" || k == "cy" || k == "r" {
				return r, false
			}
		}
		switch c.Data {
		case "circle":
			x, ok1 := qualityMorphNumber(qualityAttr(c, "cx"))
			y, ok2 := qualityMorphNumber(qualityAttr(c, "cy"))
			radius, ok3 := qualityMorphNumber(qualityAttr(c, "r"))
			if !ok1 || !ok2 || !ok3 || radius <= 0 {
				return r, false
			}
			hub = qualityMorphPoint{x, y}
			circles++
			points = append(points, qualityMorphPoint{x - radius, y - radius}, qualityMorphPoint{x + radius, y + radius})
		case "path":
			path, ok := qualityMorphParsePath(qualityAttr(c, "d"))
			if !ok || path.closed || strings.ContainsAny(path.signature, "QqCc") {
				return r, false
			}
			stroke, ok := sheet.stroke(c)
			if !ok {
				return r, false
			}
			r.stroke = math.Max(r.stroke, stroke)
			points = append(points, path.points...)
			lines = append(lines, path.lines...)
		default:
			return r, false
		}
	}
	if circles != 1 || len(lines) < 4 {
		return r, false
	}
	for _, line := range lines {
		if qualityMorphDistance(line.a, hub) < .01 && qualityMorphDistance(line.b, hub) > 8 {
			r.tips = append(r.tips, line.b)
		}
	}
	if len(r.tips) != 2 {
		return r, false
	}
	if style["animation"] != "" {
		if qualityMorphHasAnimation(n) {
			return r, false
		}
		frames, duration, ok := sheet.animation(n)
		if !ok || len(frames) > 2 {
			return r, false
		}
		if len(frames) == 1 {
			if frames[0].offset != 1 {
				return r, false
			}
		} else if frames[0].offset != 0 || frames[1].offset != 1 {
			return r, false
		}
		angles := []float64{0}
		for _, frame := range frames {
			if len(frame.values) != 1 {
				return r, false
			}
			match := qualityRotation.FindStringSubmatch(frame.values["transform"])
			if len(match) != 2 {
				return r, false
			}
			angle, ok := qualityMorphNumber(match[1])
			if !ok {
				return r, false
			}
			angles = append(angles, angle)
		}
		if len(frames) == 2 && math.Abs(angles[1]) > .001 {
			return r, false
		}
		if math.Abs(math.Abs(angles[len(angles)-1])-360) > .001 {
			return r, false
		}
		if style["transform-box"] != "fill-box" || (style["transform-origin"] != "center" && style["transform-origin"] != "50% 50%") {
			return r, false
		}
		lo, hi := qualityMorphBounds(points)
		r.center = qualityMorphPoint{(lo.x + hi.x) / 2, (lo.y + hi.y) / 2}
		r.duration = duration
	} else {
		count := 0
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode || c.Data != "animateTransform" {
				continue
			}
			count++
			if !qualityMorphSMILSimple(c) || qualityAttr(c, "type") != "rotate" || qualityAttr(c, "attributeName") != "transform" || qualityAttr(c, "values") != "" {
				return r, false
			}
			from := strings.Fields(strings.ReplaceAll(qualityAttr(c, "from"), ",", " "))
			to := strings.Fields(strings.ReplaceAll(qualityAttr(c, "to"), ",", " "))
			if len(from) != 3 || len(to) != 3 {
				return r, false
			}
			var a, b [3]float64
			for i := range a {
				var ok1, ok2 bool
				a[i], ok1 = qualityMorphNumber(from[i])
				b[i], ok2 = qualityMorphNumber(to[i])
				if !ok1 || !ok2 {
					return r, false
				}
			}
			if a[0] != 0 || math.Abs(b[0]) != 360 || a[1] != b[1] || a[2] != b[2] {
				return r, false
			}
			r.center = qualityMorphPoint{a[1], a[2]}
			r.duration, ok = qualityMorphDuration(qualityAttr(c, "dur"))
			if !ok {
				return r, false
			}
		}
		if count != 1 {
			return r, false
		}
	}
	// Include the complete pedal bars, not only their attachment points.
	r.minRadius = math.Inf(1)
	for _, tip := range r.tips {
		found := false
		r.minRadius = math.Min(r.minRadius, qualityMorphDistance(tip, r.center))
		for _, line := range lines {
			if qualityMorphDistance(line.a, tip) <= 3 && qualityMorphDistance(line.a, line.b) >= 4 && qualityMorphDistance(line.a, hub) > 8 {
				found = true
				r.minRadius = math.Min(r.minRadius, qualityMorphSegmentDistance(r.center, line))
			}
		}
		if !found {
			return r, false
		}
	}
	return r, r.minRadius > 8
}

func qualityMorphBounds(points []qualityMorphPoint) (qualityMorphPoint, qualityMorphPoint) {
	lo, hi := points[0], points[0]
	for _, p := range points[1:] {
		lo.x = math.Min(lo.x, p.x)
		lo.y = math.Min(lo.y, p.y)
		hi.x = math.Max(hi.x, p.x)
		hi.y = math.Max(hi.y, p.y)
	}
	return lo, hi
}

func qualityMorphSegmentDistance(p qualityMorphPoint, s qualityMorphSegment) float64 {
	dx, dy := s.b.x-s.a.x, s.b.y-s.a.y
	if dx*dx+dy*dy == 0 {
		return qualityMorphDistance(p, s.a)
	}
	t := math.Max(0, math.Min(1, ((p.x-s.a.x)*dx+(p.y-s.a.y)*dy)/(dx*dx+dy*dy)))
	return qualityMorphDistance(p, qualityMorphPoint{s.a.x + t*dx, s.a.y + t*dy})
}

// Parent translations are bounded, not discarded: a small bob cannot repair a
// gap larger than its entire travel. Other transforms require rendered analysis.
func qualityMorphContext(n *html.Node, sheet qualityMorphSheet) (*html.Node, float64, bool) {
	bound := 0.0
	var root *html.Node
	for n = n.Parent; n != nil; n = n.Parent {
		if n.Type != html.ElementNode {
			continue
		}
		if n.Data == "svg" {
			if root != nil {
				return nil, 0, false
			}
			root = n
		}
		if qualityMorphHasAnimation(n) || qualityAttr(n, "transform") != "" {
			return nil, 0, false
		}
		style, ok := sheet.css.style(n)
		if !ok || (style["transform"] != "" && style["transform"] != "none") {
			return nil, 0, false
		}
		if style["animation"] == "" {
			continue
		}
		frames, _, ok := sheet.animation(n)
		if !ok {
			return nil, 0, false
		}
		maxShift := 0.0
		for _, f := range frames {
			if len(f.values) != 1 {
				return nil, 0, false
			}
			v := f.values["transform"]
			if (!strings.HasPrefix(v, "translateY(") && !strings.HasPrefix(v, "translateX(")) || !strings.HasSuffix(v, ")") {
				return nil, 0, false
			}
			value := strings.TrimSpace(v[11 : len(v)-1])
			value = strings.TrimSuffix(value, "px")
			shift, ok := qualityMorphNumber(value)
			if !ok {
				return nil, 0, false
			}
			maxShift = math.Max(maxShift, math.Abs(shift))
		}
		bound += maxShift
	}
	return root, bound, root != nil
}

func qualityMorphChordMismatch(t qualityMorphTrack, r qualityMorphRotor, sheet qualityMorphSheet) bool {
	if !qualityMorphSame(t.duration, r.duration) {
		return false
	}
	root, drift, ok := qualityMorphContext(t.node, sheet)
	other, otherDrift, ok2 := qualityMorphContext(r.node, sheet)
	if !ok || !ok2 || root != other {
		return false
	}
	stroke, _ := sheet.stroke(t.node)
	for i := 0; i+1 < len(t.paths); i++ {
		a, b := t.paths[i].end, t.paths[i+1].end
		if qualityMorphDistance(t.paths[i].start, r.center) < r.minRadius*1.5 {
			continue
		}
		matched := func(p qualityMorphPoint) bool {
			for _, tip := range r.tips {
				if qualityMorphDistance(p, tip) <= 3 {
					return true
				}
			}
			return false
		}
		if !matched(a) || !matched(b) || qualityMorphDistance(a, b) < 10 {
			continue
		}
		mid := qualityMorphPoint{(a.x + b.x) / 2, (a.y + b.y) / 2}
		margin := r.minRadius - qualityMorphDistance(mid, r.center) - drift - otherDrift - (stroke+r.stroke)/2
		if margin > math.Max(3, r.minRadius*.1) {
			return true
		}
	}
	return false
}

type qualityMorphFoot struct {
	hull   []qualityMorphPoint
	stroke float64
}

func qualityMorphFeet(parent *html.Node, sheet qualityMorphSheet) []qualityMorphFoot {
	var feet []qualityMorphFoot
	for n := parent.FirstChild; n != nil; n = n.NextSibling {
		if n.Type != html.ElementNode || n.Data != "path" || !sheet.visible(n) || qualityMorphHasAnimation(n) || qualityAttr(n, "transform") != "" {
			continue
		}
		style, ok := sheet.css.style(n)
		if !ok {
			continue
		}
		moving := false
		for key := range style {
			if qualityMotionProperty(key) || key == "d" {
				moving = true
			}
		}
		if moving {
			continue
		}
		path, ok := qualityMorphParsePath(qualityAttr(n, "d"))
		if !ok || !path.closed || path.moves != 1 {
			continue
		}
		fill := sheet.property(n, "fill", "black", true)
		if fill == "none" || fill == "transparent" || fill == "?" || strings.Contains(fill, "var(") {
			continue
		}
		stroke, ok := sheet.stroke(n)
		if !ok && sheet.property(n, "stroke", "none", true) != "none" {
			continue
		}
		feet = append(feet, qualityMorphFoot{qualityMorphHull(path.points), stroke})
	}
	return feet
}

func qualityMorphDetachedFeet(tracks []qualityMorphTrack, r qualityMorphRotor, sheet qualityMorphSheet) bool {
	root, _, ok := qualityMorphContext(r.node, sheet)
	if !ok {
		return false
	}
	for i, left := range tracks {
		if !left.css || !qualityMorphSame(left.duration, r.duration) {
			continue
		}
		other, _, ok := qualityMorphContext(left.node, sheet)
		if !ok || other != root {
			continue
		}
		for _, right := range tracks[i+1:] {
			if !right.css || right.node.Parent != left.node.Parent || !qualityMorphSame(left.duration, right.duration) {
				continue
			}
			a, b := left.paths[0].end, left.paths[1].end
			if qualityMorphDistance(a, right.paths[1].end) > .01 || qualityMorphDistance(b, right.paths[0].end) > .01 || qualityMorphDistance(a, b) < 20 {
				continue
			}
			if qualityMorphDistance(a, r.center) > r.minRadius*2.5 || qualityMorphDistance(b, r.center) > r.minRadius*2.5 {
				continue
			}
			feet := qualityMorphFeet(left.node.Parent, sheet)
			if len(feet) < 2 || len(feet) > 32 {
				continue
			}
			ls, _ := sheet.stroke(left.node)
			rs, _ := sheet.stroke(right.node)
			mid := qualityMorphPoint{(a.x + b.x) / 2, (a.y + b.y) / 2}
			first, second := -1, -1
			detached := true
			for j, foot := range feet {
				if qualityMorphHullDistance(a, foot.hull) <= (ls+foot.stroke)/2 {
					first = j
				}
				if qualityMorphHullDistance(b, foot.hull) <= (rs+foot.stroke)/2 {
					second = j
				}
				// Control hull and round-cap allowance overestimate painted extent.
				if qualityMorphHullDistance(mid, foot.hull) <= (math.Max(ls, rs)+foot.stroke)/2+2 {
					detached = false
				}
			}
			if first >= 0 && second >= 0 && first != second && detached {
				return true
			}
		}
	}
	return false
}

func qualityMorphHull(points []qualityMorphPoint) []qualityMorphPoint {
	p := append([]qualityMorphPoint(nil), points...)
	sort.Slice(p, func(i, j int) bool {
		if p[i].x == p[j].x {
			return p[i].y < p[j].y
		}
		return p[i].x < p[j].x
	})
	cross := func(a, b, c qualityMorphPoint) float64 { return (b.x-a.x)*(c.y-a.y) - (b.y-a.y)*(c.x-a.x) }
	var hull []qualityMorphPoint
	for _, v := range p {
		for len(hull) >= 2 && cross(hull[len(hull)-2], hull[len(hull)-1], v) <= 0 {
			hull = hull[:len(hull)-1]
		}
		hull = append(hull, v)
	}
	lower := len(hull) + 1
	for i := len(p) - 2; i >= 0; i-- {
		for len(hull) >= lower && cross(hull[len(hull)-2], hull[len(hull)-1], p[i]) <= 0 {
			hull = hull[:len(hull)-1]
		}
		hull = append(hull, p[i])
	}
	if len(hull) > 1 {
		hull = hull[:len(hull)-1]
	}
	return hull
}

func qualityMorphHullDistance(p qualityMorphPoint, hull []qualityMorphPoint) float64 {
	if len(hull) == 0 {
		return math.Inf(1)
	}
	distance := math.Inf(1)
	inside := len(hull) >= 3
	for i, a := range hull {
		b := hull[(i+1)%len(hull)]
		if (b.x-a.x)*(p.y-a.y)-(b.y-a.y)*(p.x-a.x) < 0 {
			inside = false
		}
		distance = math.Min(distance, qualityMorphSegmentDistance(p, qualityMorphSegment{a, b}))
	}
	if inside {
		return 0
	}
	return distance
}
