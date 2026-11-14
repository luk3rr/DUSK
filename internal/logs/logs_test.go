package logs_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"dusk/internal/logs"
)

type recorder struct {
	args [][]string
	err  error
}

func (r *recorder) exec(_ context.Context, args []string, stdout, _ io.Writer) error {
	r.args = append(r.args, args)
	io.WriteString(stdout, "journal output\n")
	return r.err
}

func TestArgs(t *testing.T) {
	tests := []struct {
		name string
		o    logs.Options
		want []string
	}{
		{"defaults", logs.Options{Lines: 50}, []string{"--user", "--unit=dusk.service", "--no-pager", "--lines=50"}},
		{"follow", logs.Options{Lines: 10, Follow: true}, []string{"--user", "--unit=dusk.service", "--no-pager", "--lines=10", "--follow"}},
		{"only new lines", logs.Options{Lines: 0, Follow: true}, []string{"--user", "--unit=dusk.service", "--no-pager", "--lines=0", "--follow"}},
		{"since", logs.Options{Lines: 50, Since: "1 hour ago"}, []string{"--user", "--unit=dusk.service", "--no-pager", "--lines=50", "--since=1 hour ago"}},
		{"since cannot become an option", logs.Options{Lines: 5, Since: "--vacuum-time=1s"}, []string{"--user", "--unit=dusk.service", "--no-pager", "--lines=5", "--since=--vacuum-time=1s"}},
	}
	for _, tc := range tests {
		got, err := logs.Args(tc.o)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v %v", tc.name, got, err)
		}
	}
	if _, err := logs.Args(logs.Options{Lines: -1}); err == nil {
		t.Error("a negative line count must be rejected")
	}
}

func TestShowRunsJournalctlAndStreamsItsOutput(t *testing.T) {
	r := &recorder{}
	var out, errb bytes.Buffer
	if err := logs.Show(context.Background(), r.exec, logs.Options{Lines: 3}, &out, &errb); err != nil {
		t.Fatal(err)
	}
	if len(r.args) != 1 || out.String() != "journal output\n" {
		t.Errorf("args=%v out=%q", r.args, out.String())
	}
}

func TestShowReportsFailures(t *testing.T) {
	r := &recorder{err: errors.New("exit status 1")}
	var out, errb bytes.Buffer
	if err := logs.Show(context.Background(), r.exec, logs.Options{Lines: 3}, &out, &errb); err == nil {
		t.Error("a journalctl failure must be an error")
	}
	if err := logs.Show(context.Background(), r.exec, logs.Options{Lines: -2}, &out, &errb); err == nil || len(r.args) != 1 {
		t.Errorf("an invalid option must fail before running anything: %v %v", err, r.args)
	}
}

func TestInterruptedFollowIsNotAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	run := func(ctx context.Context, _ []string, _, _ io.Writer) error {
		cancel()
		return errors.New("signal: killed")
	}
	var out, errb bytes.Buffer
	if err := logs.Show(ctx, run, logs.Options{Follow: true}, &out, &errb); err != nil {
		t.Errorf("Ctrl+C must exit cleanly: %v", err)
	}
}

func TestExecJournalctlMissingBinaryExplainsItself(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no journalctl here
	var out, errb bytes.Buffer
	err := logs.ExecJournalctl(context.Background(), []string{"--user"}, &out, &errb)
	if err == nil || !strings.Contains(err.Error(), "needs systemd") {
		t.Errorf("err = %v", err)
	}
}
