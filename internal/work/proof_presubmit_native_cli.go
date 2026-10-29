package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"compound/internal/authority/gitrepository"
	homepkg "compound/internal/home"
)

type PresubmitProducer func(context.Context, PresubmitExecutionRequest) (ProofCheckReport, error)

func RunNativePresubmitCLI(args []string, stdout, stderr io.Writer, produce PresubmitProducer) int {
	request, err := ParsePresubmitExecutionRequest(args)
	if err != nil {
		fmt.Fprintln(stderr, "proof-check:", err)
		return 2
	}
	homePath, err := homepkg.Resolve("")
	if err == nil {
		homePath, err = filepath.EvalSymlinks(homePath)
	}
	if err != nil {
		fmt.Fprintln(stderr, "proof-check:", err)
		return 2
	}
	store, err := Open(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "proof-check:", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, runErr := store.RunPresubmitProducer(ctx, request, func(ctx context.Context, request PresubmitExecutionRequest) (ProofCheckReport, error) {
		if dropped := DropInheritedLoopbackProxy(); len(dropped) > 0 {
			fmt.Fprintf(stderr, "proof-check: native presubmit removed inherited loopback proxy (%v)\n", dropped)
		}
		source, err := gitrepository.CaptureCleanSnapshotContext(ctx, request.Repo, request.BaseRef)
		if err != nil {
			return ProofCheckReport{}, err
		}
		if err = BindPresubmitSource(ctx, source); err != nil {
			return ProofCheckReport{}, err
		}
		return produce(ctx, request)
	})
	return renderNativePresubmitExecution(result, runErr, args, stdout, stderr)
}

// FinishNativePresubmitReport keeps final source validation and rooted report
// publication inside the admitted attempt rather than after terminalization.
func FinishNativePresubmitReport(ctx context.Context, request PresubmitExecutionRequest, report ProofCheckReport, runErr error, publish func(ProofCheckReport) error) (ProofCheckReport, error) {
	if runErr == nil {
		native, ok := PresubmitExecutionFromContext(ctx)
		if !ok || native.SourceSnapshot == nil {
			runErr = fmt.Errorf("presubmit source binding unavailable")
		} else {
			runErr = gitrepository.RevalidateSnapshotContext(ctx, request.Repo, *native.SourceSnapshot)
		}
	}
	runErr = errors.Join(runErr, context.Cause(ctx))
	if runErr != nil {
		report.Status = "failed"
		report.Warnings = append(report.Warnings, runErr.Error())
	}
	return report, errors.Join(runErr, publish(report))
}

func renderNativePresubmitExecution(result PresubmitExecutionResult, runErr error, args []string, stdout, stderr io.Writer) int {
	if result.Report != nil && (runErr != nil || result.Status != "succeeded") {
		result.Report.Status = "failed"
		if runErr != nil {
			result.Report.Warnings = append(result.Report.Warnings, runErr.Error())
		}
	}
	for _, arg := range args {
		if arg == "--json" {
			body, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			fmt.Fprintln(stdout, string(body))
			break
		}
	}
	if runErr != nil {
		fmt.Fprintln(stderr, "proof-check:", runErr)
	}
	if result.Report != nil {
		fmt.Fprintln(stdout, ProofCheckPresubmitVerdictLine(*result.Report, result.Request.OutPath))
	}
	fmt.Fprintf(stderr, "presubmit execution: %s status=%s active-budget=%s result=%s\n", result.RunAttemptID, result.Status, result.Request.Budget, result.ArtifactRef)
	if runErr != nil || result.Status != "succeeded" {
		return 1
	}
	return 0
}
