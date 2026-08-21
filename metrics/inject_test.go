package metrics

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
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
		`m{a="bad \q escape"} 1`,
		`m{a="dangling\`,
	}
	for _, line := range lines {
		if _, err := parseSampleLine(line); err == nil {
			t.Errorf("parseSampleLine(%q) succeeded, want error", line)
		}
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
	if err := client.Push(context.Background()); err != nil {
		t.Fatalf("Push: %v", err)
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
	if stats.LinesSkipped != 1 {
		t.Errorf("LinesSkipped = %d, want 1 (the malformed line)", stats.LinesSkipped)
	}
	if stats.SeriesLastPush != 5 {
		t.Errorf("SeriesLastPush = %d, want 5", stats.SeriesLastPush)
	}
}

func TestFixedLabelCollisionReplacement(t *testing.T) {
	r := renderer{
		cfg: Config{}.withDefaults(),
		fixed: []label{
			{name: "instance", value: "fixed-instance"},
			{name: "job", value: "fixed-job"},
		},
	}
	if err := r.addSource([]byte(`m{job="src",instance="src",other="keep"} 1` + "\n")); err != nil {
		t.Fatalf("addSource: %v", err)
	}
	want := `m{instance="fixed-instance",job="fixed-job",other="keep"} 1` + "\n"
	if got := r.out.String(); got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}
	if strings.Count(r.out.String(), "job=") != 1 {
		t.Error("duplicate job label emitted")
	}
}

func TestPanickingSourceIsRecoveredAndSkipped(t *testing.T) {
	cs := newCaptureServer(t)
	client, err := New(testConfig(cs), panicSource{}, textSource("survivor 1\n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := client.Push(context.Background()); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if got := cs.last().body; !strings.Contains(got, "survivor") {
		t.Errorf("surviving source missing from body:\n%s", got)
	}
	if stats := client.Stats(); stats.SourcePanics != 1 {
		t.Errorf("SourcePanics = %d, want 1", stats.SourcePanics)
	}
}

func TestCommentOnlyBodyLimit(t *testing.T) {
	r := renderer{cfg: Config{MaxBodyBytes: 8}.withDefaults()}
	r.cfg.MaxBodyBytes = 8
	err := r.addSource([]byte("# a very long comment line\n"))
	var limitErr *LimitError
	if !errors.As(err, &limitErr) || limitErr.Limit != "MaxBodyBytes" {
		t.Errorf("err = %v, want *LimitError{MaxBodyBytes}", err)
	}
}
