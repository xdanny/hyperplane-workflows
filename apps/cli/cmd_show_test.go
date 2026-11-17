package main

import (
	"strings"
	"testing"
)

// "passed · 3 caveats" was three CONFIRMATIONS (S-17). `show` counts caveats,
// names the first failure outright, and keeps the three levels apart.
func TestShowReportsReviewVerdictsByLevel(t *testing.T) {
	doc := []byte(`{"sdk":{"phase":"passed","nodes":[
		{"id":"draft","title":"Draft","phase":"passed"},
		{"id":"review","title":"Review the draft","phase":"passed",
		 "findings":["CONFIRMED - Every figure carries a source.","CAVEAT - The sample size is not stated.","CONFIRMED - The dates match the brief."],
		 "findingDetails":[{"level":"confirmation","text":"CONFIRMED - Every figure carries a source."},{"level":"caveat","text":"CAVEAT - The sample size is not stated."},{"level":"confirmation","text":"CONFIRMED - The dates match the brief."}]}]}}`)
	steps := sdkReviewSteps(doc)
	if len(steps) != 1 || steps[0].Title != "Review the draft" {
		t.Fatalf("reviews: %+v", steps)
	}
	if got := reviewSummary(steps[0]); got != "passed · 1 caveat" {
		t.Errorf("summary %q, want %q", got, "passed · 1 caveat")
	}

	// A pass with no caveats says so rather than counting confirmations.
	clean := steps[0]
	clean.Details = clean.Details[:1]
	if got := reviewSummary(clean); got != "passed, no caveats" {
		t.Errorf("summary %q, want %q", got, "passed, no caveats")
	}

	// A failure leads with the failing criterion, not with a count.
	failed := reviewStep{Title: "Review", Passed: false, Details: []reviewFinding{
		{Level: "caveat", Text: "CAVEAT - Unclear audience."},
		{Level: "fail", Text: "FAIL - The revenue claim has no source."}}}
	if got := reviewSummary(failed); !strings.HasPrefix(got, "failed — FAIL - The revenue claim") {
		t.Errorf("summary %q", got)
	}
}

// A run recorded before typed findings existed keeps its plain strings, and
// `show` reports how many findings it recorded without claiming a level.
func TestShowDoesNotInventLevelsForOlderRuns(t *testing.T) {
	doc := []byte(`{"sdk":{"phase":"failed","nodes":[{"id":"review","title":"Review","phase":"failed","findings":["Evidence is limited"]}]}}`)
	steps := sdkReviewSteps(doc)
	if len(steps) != 1 || len(steps[0].Details) != 0 {
		t.Fatalf("reviews: %+v", steps)
	}
	if got := reviewSummary(steps[0]); got != "failed · 1 finding recorded" {
		t.Errorf("summary %q", got)
	}
	if steps := sdkReviewSteps(nil); steps != nil {
		t.Errorf("a run with no checkpoint reported reviews: %+v", steps)
	}
	if steps := sdkReviewSteps([]byte(`{"sdk":{"nodes":[{"id":"draft","phase":"passed"}]}}`)); steps != nil {
		t.Errorf("a run with no review findings reported reviews: %+v", steps)
	}
}
