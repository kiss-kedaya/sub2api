package service

import _ "embed"

// scheduledVisualReferenceHTML is a known-good pelican-on-a-bicycle artwork
// captured from a passing check (result #1417479). It is rendered by the same
// renderer as the candidate frames and shipped as the reference image, so the
// visual review compares the candidate against an accepted example instead of
// relying on prose alone.
//
//go:embed scheduled_test_visual_reference.html
var scheduledVisualReferenceHTML string
