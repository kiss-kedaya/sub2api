package service

import (
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/css"
	"golang.org/x/net/html"
)

type qualityCSSRule struct{ selector, property, value string }
type qualityCSS struct {
	rules       []qualityCSSRule
	rotations   map[string][]float64
	unsupported bool
}

var (
	qualitySimpleSelector = regexp.MustCompile("^(?:[a-zA-Z][a-zA-Z0-9-]*|\\*)?(?:[.#][a-zA-Z_][a-zA-Z0-9_-]*)*$")
	qualitySelectorParts  = regexp.MustCompile("[.#]?[a-zA-Z_][a-zA-Z0-9_-]*|\\*")
	qualityRotation       = regexp.MustCompile("^rotate\\(\\s*([-+]?(?:\\d*\\.)?\\d+)deg\\s*\\)$")
	qualityTranslation    = regexp.MustCompile("^translate\\(\\s*([-+]?(?:\\d*\\.)?\\d+)[ ,]+([-+]?(?:\\d*\\.)?\\d+)\\s*\\)$")
)

func scheduledTestAnimationQuality(content string) (string, string) {
	unknown := func(reason string) (string, string) { return "unknown", "quality check inconclusive: " + reason }
	doc, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return unknown("cannot parse animation document")
	}
	var nodes []*html.Node
	var styles strings.Builder
	dynamic := false
	qualityWalk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		nodes = append(nodes, n)
		if n.Data == "script" || strings.HasPrefix(strings.ToLower(n.Data), "animate") || n.Data == "set" {
			dynamic = true
		}
		for _, a := range n.Attr {
			if strings.HasPrefix(strings.ToLower(a.Key), "on") {
				dynamic = true
			}
		}
		if n.Data == "style" {
			if qualityAttr(n, "media") != "" {
				dynamic = true
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.TextNode {
					styles.WriteString(c.Data)
					styles.WriteByte('\n')
				}
			}
		}
	})
	if reason := qualityMorphFailure(doc); reason != "" {
		return "degraded", reason
	}
	if dynamic {
		return unknown("script or dynamic SVG requires rendered motion evaluation")
	}
	if len(nodes) > 5000 {
		return unknown("document exceeds local node limit")
	}
	sheet := parseQualityCSS(styles.String())
	if sheet.unsupported {
		return unknown("CSS cascade or animation is outside supported static analysis")
	}
	for _, n := range nodes {
		if n.Namespace != "svg" || (n.Data != "g" && n.Data != "path") {
			continue
		}
		label := strings.ToLower(qualityAttr(n, "id") + " " + qualityAttr(n, "class"))
		if !strings.Contains(label, "leg") && !strings.Contains(label, "wheel") && !strings.Contains(label, "pedal") && !strings.Contains(label, "crank") {
			continue
		}
		style, ok := sheet.style(n)
		if !ok {
			continue
		}
		angles, rotating := sheet.activeRotation(style)
		if !rotating {
			continue
		}
		if translate := qualityTranslation.FindStringSubmatch(qualityAttr(n, "transform")); len(translate) == 3 && len(angles) > 1 {
			x, _ := strconv.ParseFloat(translate[1], 64)
			y, _ := strconv.ParseFloat(translate[2], 64)
			if math.Abs(x)+math.Abs(y) > 1 {
				return "degraded", "quality check failed: rotating SVG limb/wheel replaces its placement transform (CSS rotation overrides SVG translation)"
			}
		}
		fullTurn := false
		if strings.Contains(label, "leg") && qualityRigidLeg(n, sheet) && qualityAngleRange(angles) > 15 {
			return "degraded", "quality check failed: a complete bent leg rotates as one rigid shape; knee and foot do not articulate through the pedal cycle"
		}
		for _, angle := range angles {
			if math.Abs(angle) >= 359 {
				fullTurn = true
			}
		}
		if !fullTurn || style["transform-box"] != "fill-box" {
			continue
		}
		if strings.Contains(label, "wheel") && qualitySeparatedWheelCenters(n) {
			return "degraded", "quality check failed: separate wheel hubs share one rotating SVG group"
		}
		if (strings.Contains(label, "pedal") || strings.Contains(label, "crank")) && qualityZeroOrigin(style["transform-origin"]) && qualityCrankHasInteriorHub(n) {
			return "degraded", "quality check failed: crank rotates around the bounding-box corner instead of its stationary hub"
		}
	}
	// Absence of a known defect is not a positive visual/kinematic assessment.
	return unknown("no proven structural defect; visual quality and leg/pedal linkage need rendered evaluation")
}

func qualityWalk(n *html.Node, visit func(*html.Node)) {
	visit(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		qualityWalk(c, visit)
	}
}

func qualityAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func qualityMotionProperty(property string) bool {
	return strings.HasPrefix(property, "animation") || strings.HasPrefix(property, "transform") ||
		property == "all" || property == "rotate" || property == "translate" || property == "scale" || property == "offset-path"
}

func qualityCSSValues(p *css.Parser) string {
	var s strings.Builder
	for _, token := range p.Values() {
		s.Write(token.Data)
	}
	return strings.TrimSpace(s.String())
}

func parseQualityCSS(text string) qualityCSS {
	sheet := qualityCSS{rotations: make(map[string][]float64)}
	type block struct {
		selector, keyframe string
		conditional        bool
		ignored            bool
	}
	var stack []block
	var selectors string
	invalidFrames := make(map[string]bool)
	p := css.NewParser(parse.NewInputString(text), false)
	for {
		kind, _, data := p.Next()
		value := qualityCSSValues(p)
		if kind == css.ErrorGrammar {
			if p.Err() != io.EOF || len(stack) != 0 {
				sheet.unsupported = true
			}
			break
		}
		switch kind {
		case css.QualifiedRuleGrammar:
			selectors += value + ","
		case css.BeginAtRuleGrammar:
			b := block{conditional: true}
			if len(stack) > 0 {
				b.ignored = stack[len(stack)-1].ignored
			}
			// Assess ordinary playback, not the accessibility opt-out of motion.
			if string(data) == "@media" && strings.Join(strings.Fields(strings.ToLower(value)), "") == "(prefers-reduced-motion:reduce)" {
				b.ignored = true
			}
			if string(data) == "@keyframes" && len(stack) == 0 {
				b = block{keyframe: value}
				sheet.rotations[value] = nil
			}
			stack = append(stack, b)
		case css.BeginRulesetGrammar:
			b := block{selector: selectors + value}
			selectors = ""
			if len(stack) > 0 {
				b.keyframe = stack[len(stack)-1].keyframe
				b.conditional = stack[len(stack)-1].conditional
				b.ignored = stack[len(stack)-1].ignored
			}
			stack = append(stack, b)
		case css.EndRulesetGrammar, css.EndAtRuleGrammar:
			if len(stack) == 0 {
				sheet.unsupported = true
			} else {
				stack = stack[:len(stack)-1]
			}
		case css.DeclarationGrammar:
			property := string(data)
			if !qualityMotionProperty(property) {
				continue
			}
			if len(stack) == 0 {
				sheet.unsupported = true
				continue
			}
			b := stack[len(stack)-1]
			if b.ignored {
				continue
			}
			if b.conditional {
				// Speed overrides cannot fix an incorrect rotation axis or placement.
				if property != "animation-duration" && property != "animation-iteration-count" {
					sheet.unsupported = true
				}
				continue
			}
			if b.keyframe != "" {
				m := qualityRotation.FindStringSubmatch(value)
				if property != "transform" || len(m) != 2 {
					invalidFrames[b.keyframe] = true
					continue
				}
				angle, _ := strconv.ParseFloat(m[1], 64)
				sheet.rotations[b.keyframe] = append(sheet.rotations[b.keyframe], angle)
				continue
			}
			for _, selector := range strings.Split(b.selector, ",") {
				selector = strings.TrimSpace(selector)
				if selector == "" || !qualitySimpleSelector.MatchString(selector) {
					if strings.HasSuffix(selector, "::before") || strings.HasSuffix(selector, "::after") {
						continue
					}
					if property != "animation-delay" {
						sheet.unsupported = true
					}
					continue
				}
				sheet.rules = append(sheet.rules, qualityCSSRule{selector, property, value})
			}
		}
	}
	for name := range invalidFrames {
		delete(sheet.rotations, name)
	}
	return sheet
}

func qualitySelectorMatch(n *html.Node, selector string) (int, bool) {
	score := 0
	for _, part := range qualitySelectorParts.FindAllString(selector, -1) {
		switch part[0] {
		case '#':
			if qualityAttr(n, "id") != part[1:] {
				return 0, false
			}
			score += 100
		case '.':
			found := false
			for _, class := range strings.Fields(qualityAttr(n, "class")) {
				if class == part[1:] {
					found = true
				}
			}
			if !found {
				return 0, false
			}
			score += 10
		case '*':
		default:
			if n.Data != part {
				return 0, false
			}
			score++
		}
	}
	return score, true
}

func qualityAngleRange(angles []float64) float64 {
	if len(angles) == 0 {
		return 0
	}
	lo, hi := angles[0], angles[0]
	for _, angle := range angles[1:] {
		lo = math.Min(lo, angle)
		hi = math.Max(hi, angle)
	}
	return hi - lo
}

func qualityRigidLeg(n *html.Node, sheet qualityCSS) bool {
	if n == nil || n.Parent == nil {
		return false
	}
	// A valid leg animation may be split into independently animated thigh,
	// shin, and foot elements. Flag only a repeated, whole-path leg motion.
	root := n.Parent
	for root.Parent != nil && root.Parent.Data != "svg" {
		root = root.Parent
	}
	legNodes, rigidNodes := 0, 0
	qualityWalk(root, func(candidate *html.Node) {
		if candidate.Type != html.ElementNode {
			return
		}
		label := strings.ToLower(qualityAttr(candidate, "id") + " " + qualityAttr(candidate, "class"))
		if !strings.Contains(label, "leg") {
			return
		}
		style, ok := sheet.style(candidate)
		if !ok {
			return
		}
		angles, rotating := sheet.activeRotation(style)
		if !rotating || qualityAngleRange(angles) <= 15 {
			return
		}
		legNodes++
		articulated := false
		pathCount := 0
		if candidate.Data == "path" && qualityPathHasMultipleSegments(qualityAttr(candidate, "d")) {
			pathCount++
		}
		qualityWalk(candidate, func(child *html.Node) {
			if child == candidate || child.Type != html.ElementNode {
				return
			}
			if child.Data == "path" && qualityPathHasMultipleSegments(qualityAttr(child, "d")) {
				pathCount++
			}
			childStyle, childOK := sheet.style(child)
			if childOK && childStyle["animation"] != "" && childStyle["animation"] != "none" {
				articulated = true
			}
		})
		if !articulated && pathCount > 0 {
			rigidNodes++
		}
	})
	return legNodes >= 2 && rigidNodes >= 2
}

func qualityPathHasMultipleSegments(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	commands := 0
	for i := 0; i < len(path); i++ {
		if strings.ContainsRune("MmLlHhVvQqTtCcSsAa", rune(path[i])) {
			commands++
		}
	}
	return commands >= 3
}

func (sheet qualityCSS) style(n *html.Node) (map[string]string, bool) {
	values := make(map[string]string)
	scores := make(map[string]int)
	apply := func(property, value string, score int) {
		if strings.HasSuffix(value, "!important") {
			value = strings.TrimSpace(strings.TrimSuffix(value, "!important"))
			score += 10000
		}
		if previous, ok := scores[property]; !ok || score >= previous {
			scores[property] = score
			values[property] = value
		}
	}
	for _, rule := range sheet.rules {
		if score, ok := qualitySelectorMatch(n, rule.selector); ok {
			apply(rule.property, rule.value, score)
		}
	}
	p := css.NewParser(parse.NewInputString(qualityAttr(n, "style")), true)
	for {
		kind, _, data := p.Next()
		if kind == css.ErrorGrammar {
			if p.Err() != io.EOF {
				return nil, false
			}
			break
		}
		if kind == css.DeclarationGrammar {
			apply(string(data), qualityCSSValues(p), 1000)
		}
	}
	for key, value := range values {
		if !qualityMotionProperty(key) {
			continue
		}
		if strings.Contains(value, "var(") || strings.Contains(value, "inherit") || strings.Contains(value, "revert") {
			return nil, false
		}
		if key == "all" || key == "rotate" || key == "translate" || key == "scale" || key == "offset-path" {
			return nil, false
		}
	}
	return values, true
}

func (sheet qualityCSS) activeRotation(style map[string]string) ([]float64, bool) {
	// Longhand/shorthand interactions and additive composition need a browser.
	for key := range style {
		if strings.HasPrefix(key, "animation-") && key != "animation-delay" {
			return nil, false
		}
	}
	value := style["animation"]
	if strings.Contains(value, ",") || !strings.Contains(value, "infinite") {
		return nil, false
	}
	if strings.Contains(value, "paused") || style["transform"] != "" {
		return nil, false
	}
	var angles []float64
	positiveDuration := false
	for _, field := range strings.Fields(value) {
		if a, ok := sheet.rotations[field]; ok {
			angles = a
		}
		if strings.HasSuffix(field, "s") {
			duration, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSuffix(field, "ms"), "s"), 64)
			if err == nil && duration > 0 {
				positiveDuration = true
			}
		}
	}
	return angles, len(angles) > 0 && positiveDuration
}

func qualityZeroOrigin(value string) bool {
	return value == "0 0" || value == "0% 0%" || value == "0px 0px" || value == "left top" || value == "top left"
}

func qualityCircle(n *html.Node) (float64, float64, float64, bool) {
	if n.Type != html.ElementNode || n.Data != "circle" || qualityAttr(n, "transform") != "" || qualityAttr(n, "style") != "" || qualityAttr(n, "class") != "" {
		return 0, 0, 0, false
	}
	x, e1 := strconv.ParseFloat(qualityAttr(n, "cx"), 64)
	y, e2 := strconv.ParseFloat(qualityAttr(n, "cy"), 64)
	r, e3 := strconv.ParseFloat(qualityAttr(n, "r"), 64)
	return x, y, r, e1 == nil && e2 == nil && e3 == nil && r > 0
}

func qualitySeparatedWheelCenters(n *html.Node) bool {
	type circle struct{ x, y, r float64 }
	var circles []circle
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		x, y, r, ok := qualityCircle(c)
		if !ok {
			continue
		}
		for _, prev := range circles {
			if math.Abs(prev.r-r) < 1 && math.Abs(prev.y-y) < 1 && math.Abs(prev.x-x) > 2*r {
				return true
			}
		}
		circles = append(circles, circle{x, y, r})
	}
	return false
}

func qualityCrankCoordinates(path string) ([8]float64, bool) {
	var values [8]float64
	// SVG permits adjacent signed coordinates, e.g. l-34-29.
	for i, command := range []byte{'M', 'l', 'M', 'l'} {
		path = strings.TrimSpace(path)
		if len(path) == 0 || path[0] != command {
			return values, false
		}
		path = path[1:]
		for j := 0; j < 2; j++ {
			path = strings.TrimLeft(path, " ,\t\r\n")
			n := parse.Number([]byte(path))
			if n == 0 {
				return values, false
			}
			v, err := strconv.ParseFloat(path[:n], 64)
			if err != nil {
				return values, false
			}
			values[2*i+j] = v
			path = path[n:]
		}
	}
	return values, strings.TrimSpace(path) == ""
}

func qualityCrankHasInteriorHub(n *html.Node) bool {
	if n.Parent == nil {
		return false
	}
	// Require a stationary sibling hub and two opposed straight crank arms.
	// A corner origin alone is valid for a hinge, so it is not evidence.
	for hub := n.Parent.FirstChild; hub != nil; hub = hub.NextSibling {
		x, y, _, ok := qualityCircle(hub)
		if !ok {
			continue
		}
		for arm := n.FirstChild; arm != nil; arm = arm.NextSibling {
			if arm.Data != "path" || qualityAttr(arm, "transform") != "" {
				continue
			}
			v, valid := qualityCrankCoordinates(qualityAttr(arm, "d"))
			if valid && math.Abs(v[0]-x) < .01 && math.Abs(v[1]-y) < .01 && math.Abs(v[4]-x) < .01 && math.Abs(v[5]-y) < .01 && v[2]*v[6] < 0 && v[3]*v[7] < 0 {
				return true
			}
		}
	}
	return false
}
