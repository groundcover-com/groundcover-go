package metrics

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf8"
)

// textSource is an MetricSource emitting a fixed exposition text.
type textSource string

func (s textSource) WritePrometheus(w io.Writer) {
	_, _ = io.WriteString(w, string(s))
}

// panicSource is an MetricSource that always panics.
type panicSource struct{}

func (panicSource) WritePrometheus(io.Writer) { panic("source panic") }

func TestParseSampleLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want sample
	}{
		{
			"unlabeled",
			"my_metric 42",
			sample{name: "my_metric", value: "42"},
		},
		{
			"labeled",
			`http_requests_total{method="GET",code="200"} 1027`,
			sample{name: "http_requests_total", labels: []label{{"method", "GET"}, {"code", "200"}}, value: "1027"},
		},
		{
			"escaped value",
			`m{msg="a \"quoted\" \\ backslash\nnewline"} 1`,
			sample{name: "m", labels: []label{{"msg", "a \"quoted\" \\ backslash\nnewline"}}, value: "1"},
		},
		{
			"timestamp",
			"m 3.14 1700000000000",
			sample{name: "m", value: "3.14", timestamp: "1700000000000"},
		},
		{
			"labeled with timestamp",
			`m{a="b"} -1e9 -42`,
			sample{name: "m", labels: []label{{"a", "b"}}, value: "-1e9", timestamp: "-42"},
		},
		{
			"special values",
			"m +Inf",
			sample{name: "m", value: "+Inf"},
		},
		{
			"empty label set",
			"m{} 5",
			sample{name: "m", value: "5"},
		},
		{
			"trailing comma",
			`m{a="b",} 5`,
			sample{name: "m", labels: []label{{"a", "b"}}, value: "5"},
		},
		{
			"colon in name",
			"ns:sub:metric 1",
			sample{name: "ns:sub:metric", value: "1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSampleLine(tt.line)
			if err != nil {
				t.Fatalf("parseSampleLine(%q) error: %v", tt.line, err)
			}
			if got.name != tt.want.name || got.value != tt.want.value || got.timestamp != tt.want.timestamp {
				t.Errorf("parseSampleLine(%q) = %+v, want %+v", tt.line, got, tt.want)
			}
			if len(got.labels) != len(tt.want.labels) {
				t.Fatalf("labels = %+v, want %+v", got.labels, tt.want.labels)
			}
			for i := range got.labels {
				if got.labels[i] != tt.want.labels[i] {
					t.Errorf("label[%d] = %+v, want %+v", i, got.labels[i], tt.want.labels[i])
				}
			}
		})
	}
}

func TestParseSampleLineMalformed(t *testing.T) {
	lines := []string{
		"",
		"just_a_name",
		"1leading_digit 2",
		`m{a=b} 1`,
		`m{a="unterminated} 1`,
		`m{a="b" 1`,
		`m{a="b"} not-a-number`,
		`m{a="b"} 1 not-a-timestamp`,
		`m{a="b"} 1 2 3`,
		`m{bad-label="b"} 1`,
		`m{a="1",a="2"} 1`,
		`m{a="bad \q escape"} 1`,
		`m{a="dangling\`,
	}
	for _, line := range lines {
		if _, err := parseSampleLine(line); err == nil {
			t.Errorf("parseSampleLine(%q) succeeded, want error", line)
		}
	}
}

func TestMetadataIsDeduplicatedAcrossSources(t *testing.T) {
	cs := newCaptureServer(t)
	first := textSource("# HELP shared_metric help\n# TYPE shared_metric gauge\nshared_metric{id=\"a\"} 1\n")
	second := textSource("# HELP shared_metric help\n# TYPE shared_metric gauge\nshared_metric{id=\"b\"} 2\n")
	client, err := New(testConfig(cs), first, second)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := client.Push(context.Background()); err != nil {
		t.Fatalf("Push: %v", err)
	}
	body := cs.last().body
	if got := strings.Count(body, "# HELP shared_metric"); got != 1 {
		t.Fatalf("HELP declarations = %d, body:\n%s", got, body)
	}
	if got := strings.Count(body, "# TYPE shared_metric"); got != 1 {
		t.Fatalf("TYPE declarations = %d, body:\n%s", got, body)
	}
}

func TestMalformedAndLateMetadataIsDroppedWithoutPoisoningSamples(t *testing.T) {
	cs := newCaptureServer(t)
	source := textSource(strings.Join([]string{
		"# ordinary comment survives",
		"# TYPE broken nonsense",
		"# HELP missing_text",
		"# TYPE too_many gauge trailing",
		"late_metric 1",
		"# TYPE late_metric gauge",
		"# TYPE healthy_metric counter",
		"healthy_metric_total 2",
	}, "\n") + "\n")
	client, err := New(testConfig(cs), source)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pushErr := client.Push(context.Background())
	if pushErr == nil || !strings.Contains(pushErr.Error(), "malformed metadata") {
		t.Fatalf("Push error = %v, want malformed metadata attribution", pushErr)
	}

	body := cs.last().body
	for _, bad := range []string{"nonsense", "missing_text", "too_many", "# TYPE late_metric"} {
		if strings.Contains(body, bad) {
			t.Errorf("invalid metadata %q reached the body:\n%s", bad, body)
		}
	}
	for _, want := range []string{
		"# ordinary comment survives",
		"late_metric{",
		"# TYPE healthy_metric counter",
		"healthy_metric_total{",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("healthy content %q missing:\n%s", want, body)
		}
	}
	if stats := client.Stats(); stats.MalformedLines != 4 || stats.PartialPushes != 1 {
		t.Errorf("Stats = %+v, want four malformed lines and one partial push", stats)
	}
}

func TestIndentedPrometheusCommentsAreAccepted(t *testing.T) {
	cs := newCaptureServer(t)
	source := textSource("  # TYPE indented_metric gauge\n  indented_metric 9\n")
	client, err := New(testConfig(cs), source)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := client.Push(context.Background()); err != nil {
		t.Fatalf("Push: %v", err)
	}
	body := cs.last().body
	if !strings.Contains(body, "# TYPE indented_metric gauge") || !strings.Contains(body, "indented_metric{") {
		t.Fatalf("indented exposition dropped:\n%s", body)
	}
}

// A source line with arbitrary bytes is dropped and counted, and the healthy
// lines around it are still delivered as a valid UTF-8 body.
func TestNonUTF8SourceLineIsDroppedNotForwarded(t *testing.T) {
	cs := newCaptureServer(t)
	source := textSource("good_metric 1\nbad_metric{path=\"/a\xff\xfeb\"} 2\nother_metric 3\n")
	client, err := New(testConfig(cs), source)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = client.Push(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not valid UTF-8") {
		t.Fatalf("Push error = %v, want a UTF-8 report", err)
	}
	body := cs.last().body
	if !utf8.ValidString(body) {
		t.Fatalf("delivered body is not valid UTF-8: %q", body)
	}
	if !strings.Contains(body, "good_metric{") || !strings.Contains(body, "other_metric{") {
		t.Fatalf("healthy samples dropped:\n%s", body)
	}
	if strings.Contains(body, "bad_metric") {
		t.Fatalf("invalid line forwarded:\n%s", body)
	}
	if stats := client.Stats(); stats.MalformedLines != 1 {
		t.Fatalf("MalformedLines = %d, want 1", stats.MalformedLines)
	}
}

// Go's float parser takes forms the exposition format does not. Forwarding one
// would abort the backend's parse of the whole body, so such a line is dropped
// and counted like any other malformed sample while its neighbours survive.
func TestSampleValuesGoAcceptsButPrometheusRejects(t *testing.T) {
	cs := newCaptureServer(t)
	source := textSource(strings.Join([]string{
		`good_metric 1`,
		`separated_metric 1_000`,
		`hexfloat_metric 0x1p3`,
		`exponent_metric 1e3`,
		`infinite_metric +Inf`,
		`nan_metric NaN`,
		`fractional_metric .5`,
	}, "\n") + "\n")
	client, err := New(testConfig(cs), source)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := client.Push(context.Background()); err == nil {
		t.Fatal("Push = nil, want the out-of-spec values reported")
	}

	body := cs.last().body
	for _, name := range []string{"separated_metric", "hexfloat_metric"} {
		if strings.Contains(body, name) {
			t.Errorf("%s forwarded:\n%s", name, body)
		}
	}
	for _, name := range []string{"good_metric", "exponent_metric", "infinite_metric", "nan_metric", "fractional_metric"} {
		if !strings.Contains(body, name) {
			t.Errorf("%s dropped:\n%s", name, body)
		}
	}
	if stats := client.Stats(); stats.MalformedLines != 2 {
		t.Errorf("MalformedLines = %d, want 2", stats.MalformedLines)
	}
}

func TestInjectionThroughPush(t *testing.T) {
	cs := newCaptureServer(t)
	source := textSource(strings.Join([]string{
		`# HELP src_metric A help line.`,
		`# TYPE src_metric counter`,
		`src_metric 7`,
		`src_labeled{region="eu",job="original-job"} 1`,
		`src_escaped{msg="say \"hi\"\n"} 2`,
		`src_stamped 3 1700000000000`,
		`this line is malformed!!!`,
		`src_hist_bucket{le="+Inf"} 4`,
	}, "\n") + "\n")

	cfg := testConfig(cs)
	cfg.ExtraLabels = map[string]string{"env": "prod"}
	client, err := New(cfg, source)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The malformed line makes this a partial push: delivery still happens
	// and Push reports the failing source with the line number.
	pushErr := client.Push(context.Background())
	if pushErr == nil {
		t.Fatal("Push = nil, want aggregate error for the malformed line")
	}
	if !strings.Contains(pushErr.Error(), `metric source "source[0]": malformed sample at line 7`) {
		t.Errorf("Push error = %v, want malformed-line attribution", pushErr)
	}

	want := strings.Join([]string{
		`# HELP src_metric A help line.`,
		`# TYPE src_metric counter`,
		`src_metric{env="prod",instance="test-instance",job="test-job"} 7`,
		`src_labeled{env="prod",instance="test-instance",job="test-job",region="eu"} 1`,
		`src_escaped{env="prod",instance="test-instance",job="test-job",msg="say \"hi\"\n"} 2`,
		`src_stamped{env="prod",instance="test-instance",job="test-job"} 3 1700000000000`,
		`src_hist_bucket{env="prod",instance="test-instance",job="test-job",le="+Inf"} 4`,
	}, "\n") + "\n"
	if got := cs.last().body; got != want {
		t.Errorf("body:\n%s\nwant:\n%s", got, want)
	}

	stats := client.Stats()
	if stats.MalformedLines != 1 {
		t.Errorf("MalformedLines = %d, want 1 (the malformed line)", stats.MalformedLines)
	}
	if stats.SeriesLastPush != 5 {
		t.Errorf("SeriesLastPush = %d, want 5", stats.SeriesLastPush)
	}
	if stats.PartialPushes != 1 || stats.PushFailures != 0 {
		t.Errorf("partial accounting wrong: %+v", stats)
	}
	if stats.SourceFailures != 1 {
		t.Errorf("SourceFailures = %d, want 1", stats.SourceFailures)
	}
	// One source label (job="original-job") was replaced by a fixed label.
	if stats.LabelOverrides != 1 {
		t.Errorf("LabelOverrides = %d, want 1", stats.LabelOverrides)
	}
}

func TestFixedLabelCollisionReplacement(t *testing.T) {
	r := renderer{
		cfg: Config{}.withDefaults(),
		fixed: []label{
			{name: "instance", value: "fixed-instance"},
			{name: "job", value: "fixed-job"},
		},
		seen: make(map[string]string),
	}
	srcErrs, err := r.addSource("test", []byte(`m{job="src",instance="src",other="keep"} 1`+"\n"))
	if err != nil || len(srcErrs) != 0 {
		t.Fatalf("addSource: fatal=%v srcErrs=%v", err, srcErrs)
	}
	want := `m{instance="fixed-instance",job="fixed-job",other="keep"} 1` + "\n"
	if got := r.out.String(); got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}
	if strings.Count(r.out.String(), "job=") != 1 {
		t.Error("duplicate job label emitted")
	}
	if r.overrides != 2 {
		t.Errorf("overrides = %d, want 2 (job and instance replaced)", r.overrides)
	}
}

func TestPanickingSourceIsRecoveredAndSkipped(t *testing.T) {
	cs := newCaptureServer(t)
	client, err := New(testConfig(cs), panicSource{}, textSource("survivor 1\n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pushErr := client.Push(context.Background())
	if pushErr == nil || !strings.Contains(pushErr.Error(), `metric source "source[0]": panic while rendering`) {
		t.Fatalf("Push error = %v, want panic attribution for source[0]", pushErr)
	}
	if got := cs.last().body; !strings.Contains(got, "survivor") {
		t.Errorf("surviving source missing from body:\n%s", got)
	}
	stats := client.Stats()
	if stats.SourcePanics != 1 {
		t.Errorf("SourcePanics = %d, want 1", stats.SourcePanics)
	}
	if stats.PartialPushes != 1 || stats.SourceFailures != 1 {
		t.Errorf("partial accounting wrong: %+v", stats)
	}
}

func TestCommentOnlyBodyLimit(t *testing.T) {
	r := renderer{cfg: Config{MaxBodyBytes: 8}.withDefaults(), seen: make(map[string]string)}
	r.cfg.MaxBodyBytes = 8
	_, err := r.addSource("test", []byte("# a very long comment line\n"))
	var limitErr *LimitError
	if !errors.As(err, &limitErr) || limitErr.Limit != "MaxBodyBytes" {
		t.Errorf("err = %v, want *LimitError{MaxBodyBytes}", err)
	}
}
