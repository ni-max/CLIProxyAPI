package antigravity

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestFetchProjectIDFromLoadCodeAssist(t *testing.T) {
	auth := NewAntigravityAuth(nil, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist" {
			t.Fatalf("unexpected request URL: %s", req.URL.String())
		}
		assertLoadCodeAssistHeaders(t, req)
		assertJSONContains(t, req, `"ideType":"ANTIGRAVITY"`)
		return jsonResponse(`{"cloudaicompanionProject":"cogent-snow-4mnnp"}`), nil
	})})

	projectID, err := auth.FetchProjectID(context.Background(), "access-token")
	if err != nil {
		t.Fatalf("FetchProjectID error: %v", err)
	}
	if projectID != "cogent-snow-4mnnp" {
		t.Fatalf("projectID = %q", projectID)
	}
}

func TestFetchProjectIDFallsBackToDailyOnboardUser(t *testing.T) {
	var sawOnboard bool
	auth := NewAntigravityAuth(nil, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist":
			assertLoadCodeAssistHeaders(t, req)
			return jsonResponse(`{"allowedTiers":[{"id":"free-tier","isDefault":true}]}`), nil
		case "https://daily-cloudcode-pa.googleapis.com/v1internal:onboardUser":
			sawOnboard = true
			assertOnboardUserHeaders(t, req)
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			want := `{"metadata":{"ideType":"ANTIGRAVITY","platform":"PLATFORM_UNSPECIFIED","pluginType":"GEMINI"},"tierId":"free-tier"}`
			if string(body) != want {
				t.Fatalf("onboardUser body = %s, want %s", body, want)
			}
			return jsonResponse(`{
				"done": true,
				"response": {
					"cloudaicompanionProject": {
						"id": "cogent-snow-4mnnp",
						"name": "cogent-snow-4mnnp",
						"projectNumber": "22597072101"
					}
				}
			}`), nil
		default:
			t.Fatalf("unexpected request URL: %s", req.URL.String())
			return nil, nil
		}
	})})

	projectID, err := auth.FetchProjectID(context.Background(), "access-token")
	if err != nil {
		t.Fatalf("FetchProjectID error: %v", err)
	}
	if !sawOnboard {
		t.Fatalf("expected onboardUser fallback")
	}
	if projectID != "cogent-snow-4mnnp" {
		t.Fatalf("projectID = %q", projectID)
	}
}

func TestFetchProjectIDUpstreamForbiddenReturnsStatus403(t *testing.T) {
	auth := NewAntigravityAuth(nil, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":{"code":403,"message":"The caller does not have permission"}}`)),
		}, nil
	})})

	_, err := auth.FetchProjectID(context.Background(), "access-token")
	if err == nil {
		t.Fatalf("expected error from 403 response")
	}
	type statusCoder interface {
		StatusCode() int
	}
	sc, ok := err.(statusCoder)
	if !ok || sc.StatusCode() != http.StatusForbidden {
		t.Fatalf("expected status code %d, got %T (%v)", http.StatusForbidden, err, err)
	}
}

func assertLoadCodeAssistHeaders(t *testing.T, req *http.Request) {
	t.Helper()
	if got := req.Header.Get("Authorization"); got != "Bearer access-token" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := req.Header.Get("Accept"); got != "*/*" {
		t.Fatalf("Accept = %q", got)
	}
	if got := req.Header.Get("X-Goog-Api-Client"); got != "" {
		t.Fatalf("X-Goog-Api-Client = %q, want empty", got)
	}
	userAgent := req.Header.Get("User-Agent")
	if !strings.HasPrefix(userAgent, "antigravity/hub/") {
		t.Fatalf("User-Agent = %q", userAgent)
	}
	if strings.Contains(userAgent, "google-api-nodejs-client/") {
		t.Fatalf("User-Agent = %q", userAgent)
	}
}

func assertOnboardUserHeaders(t *testing.T, req *http.Request) {
	t.Helper()
	if got := req.Header.Get("Authorization"); got != "Bearer access-token" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := req.Header.Get("Accept"); got != "*/*" {
		t.Fatalf("Accept = %q", got)
	}
	if got := req.Header.Get("X-Goog-Api-Client"); got != "gl-node/22.21.1" {
		t.Fatalf("X-Goog-Api-Client = %q", got)
	}
	userAgent := req.Header.Get("User-Agent")
	if !strings.HasPrefix(userAgent, "antigravity/hub/") {
		t.Fatalf("User-Agent = %q", userAgent)
	}
	if !strings.Contains(userAgent, "google-api-nodejs-client/10.3.0") {
		t.Fatalf("User-Agent = %q", userAgent)
	}
}

func assertJSONContains(t *testing.T, req *http.Request, want string) {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	bodyText := string(body)
	req.Body = io.NopCloser(strings.NewReader(bodyText))
	if !strings.Contains(bodyText, want) {
		t.Fatalf("body missing %s: %s", want, bodyText)
	}
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestFetchProjectIDEligibility(t *testing.T) {
	const defaultTier = `"allowedTiers":[{"id":" offered-tier ","isDefault":true}]`
	tests := []struct {
		name, load, wantErr string
		onboard             bool
	}{
		{"offered default", `{` + defaultTier + `}`, "", true},
		{"existing string", `{"cloudaicompanionProject":" project-fixture "}`, "", false},
		{"existing object", `{"cloudaicompanionProject":{"id":" project-fixture "}}`, "", false},
		{"existing projectId alias", `{"projectId":"project-fixture"}`, "", false},
		{"existing project alias", `{"project":{"id":"project-fixture"}}`, "", false},
		{"no offered tier", `{}`, "no offered default tier", false},
		{"current is not an offer", `{"currentTier":{"id":"free-tier"}}`, "no offered default tier", false},
		{"paid is not an offer", `{"paidTier":{"id":"paid-tier"}}`, "no offered default tier", false},
		{"name is not an ID", `{"allowedTiers":[{"name":"Free","isDefault":true}]}`, "no offered default tier", false},
		{"not default", `{"allowedTiers":[{"id":"free-tier","isDefault":false}]}`, "no offered default tier", false},
		{"empty ID", `{"allowedTiers":[{"id":" ","isDefault":true}]}`, "no offered default tier", false},
		{"matching rejection", `{` + defaultTier + `,"ineligibleTiers":[{"tier":{"id":"offered-tier"},"reasonCode":"UNKNOWN-private-sentinel","reasonMessage":"private-sentinel"}]}`, "eligibility rejected for requested tier", false},
		{"unscoped rejection", `{` + defaultTier + `,"ineligibleTiers":[{"reasonCode":"private-sentinel"}]}`, "eligibility rejected for requested tier", false},
		{"current rejection with existing project", `{"cloudaicompanionProject":"project-fixture","currentTier":{"id":"current-tier"},"ineligibleTiers":[{"tier":{"id":"current-tier"}}]}`, "eligibility rejected for requested tier", false},
		{"legacy string current rejection", `{"cloudaicompanionProject":"project-fixture","currentTier":"current-tier","ineligibleTiers":[{"tier":"current-tier"}]}`, "eligibility rejected for requested tier", false},
		{"unrelated account code allows default", `{` + defaultTier + `,"ineligibleTiers":[{"tier":{"id":"paid-tier"},"reasonCode":"INELIGIBLE_ACCOUNT"}]}`, "", true},
		{"unrelated paid allows existing current", `{"cloudaicompanionProject":"project-fixture","currentTier":{"id":"current-tier"},"ineligibleTiers":[{"tier":{"id":"paid-tier"},"reasonCode":"INELIGIBLE_ACCOUNT"}]}`, "", false},
		{"unrelated paid is not an offer", `{"ineligibleTiers":[{"tier":{"id":"paid-tier"},"reasonCode":"INELIGIBLE_ACCOUNT"}]}`, "no offered default tier", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			auth := NewAntigravityAuth(nil, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					if req.URL.String() != "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist" {
						t.Fatalf("unexpected load URL: %s", req.URL)
					}
					return jsonResponse(tt.load), nil
				}
				if !tt.onboard || calls != 2 || req.URL.String() != "https://daily-cloudcode-pa.googleapis.com/v1internal:onboardUser" {
					t.Fatalf("unexpected onboarding or retry: %s", req.URL)
				}
				assertJSONContains(t, req, `"tierId":"offered-tier"`)
				return jsonResponse(`{"done":true,"response":{"cloudaicompanionProject":"project-fixture"}}`), nil
			})})
			project, err := auth.FetchProjectID(context.Background(), "access-token")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || strings.Contains(err.Error(), "private-sentinel") || project != "" {
					t.Fatalf("project = %q, error = %v, want safe %q error", project, err, tt.wantErr)
				}
			} else if err != nil || project != "project-fixture" {
				t.Fatalf("project = %q, error = %v", project, err)
			}
			wantCalls := 1
			if tt.onboard {
				wantCalls++
			}
			if calls != wantCalls {
				t.Fatalf("calls = %d, want %d", calls, wantCalls)
			}
		})
	}
}

func TestProjectDiscoveryOperationErrors(t *testing.T) {
	tests := []struct {
		name, body, wantErr string
		load                bool
	}{
		{"load error with project", `{"error":{"message":"private-sentinel"},"cloudaicompanionProject":"private-sentinel"}`, "loadCodeAssist: upstream operation rejected", true},
		{"not done error", `{"done":false,"error":{"code":403,"message":"private-sentinel"}}`, "onboardUser: upstream operation rejected", false},
		{"done error with project", `{"done":true,"error":{"message":"private-sentinel"},"response":{"cloudaicompanionProject":"private-sentinel"}}`, "onboardUser: upstream operation rejected", false},
		{"nested error with project", `{"done":true,"response":{"error":{"message":"private-sentinel"},"cloudaicompanionProject":"private-sentinel"}}`, "onboardUser: upstream operation rejected", false},
		{"missing project", `{"done":true,"response":{}}`, "no project_id", false},
		{"empty project", `{"done":true,"response":{"cloudaicompanionProject":{"id":" "}}}`, "no project_id", false},
		{"null error valid project", `{"error":null,"done":true,"response":{"error":null,"projectId":"project-fixture"}}`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			auth := NewAntigravityAuth(nil, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if calls != 1 {
					t.Fatal("terminal result must not retry or onboard")
				}
				return jsonResponse(tt.body), nil
			})})
			var project string
			var err error
			if tt.load {
				project, err = auth.FetchProjectID(context.Background(), "access-token")
			} else {
				project, err = auth.OnboardUser(context.Background(), "access-token", "offered-tier")
			}
			if tt.wantErr == "" {
				if err != nil || project != "project-fixture" {
					t.Fatalf("project = %q, error = %v", project, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) || strings.Contains(err.Error(), "private-sentinel") || project != "" {
				t.Fatalf("project = %q, error = %v, want safe %q error", project, err, tt.wantErr)
			}
		})
	}
}

func TestProjectDiscoveryHTTPErrorIsSafe(t *testing.T) {
	for _, onboard := range []bool{false, true} {
		auth := NewAntigravityAuth(nil, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			resp := jsonResponse(`{"error":{"message":"private-sentinel","reason":"UNKNOWN-private-sentinel"}}`)
			resp.StatusCode = http.StatusForbidden
			return resp, nil
		})})
		var err error
		if onboard {
			_, err = auth.OnboardUser(context.Background(), "access-token", "offered-tier")
		} else {
			_, err = auth.FetchProjectID(context.Background(), "access-token")
		}
		sc, ok := err.(*HTTPStatusError)
		if !ok || sc.StatusCode() != http.StatusForbidden || strings.Contains(err.Error(), "private-sentinel") {
			t.Fatalf("expected safe HTTPStatusError 403, got %v", err)
		}
	}
}

func TestProjectDiscoveryUntrustedErrorsAreSafe(t *testing.T) {
	for _, onboard := range []bool{false, true} {
		for _, transportFailure := range []bool{false, true} {
			auth := NewAntigravityAuth(nil, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				if transportFailure {
					return nil, errors.New("private-sentinel")
				}
				return jsonResponse(`{"private-sentinel":invalid}`), nil
			})})
			var err error
			if onboard {
				_, err = auth.OnboardUser(context.Background(), "access-token", "offered-tier")
			} else {
				_, err = auth.FetchProjectID(context.Background(), "access-token")
			}
			if err == nil || strings.Contains(err.Error(), "private-sentinel") {
				t.Fatalf("expected safe error, got %v", err)
			}
		}
	}
}
