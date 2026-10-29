package work

import "context"

// RunNonCertifyingProofGates preserves existing development/presubmit selection.
// Native execution adds durable checkpoints without changing the gate commands.
func RunNonCertifyingProofGates(ctx context.Context, report ProofCheckReport, steps []ProofCheckStep, run func(context.Context, ProofCheckStep) ProofCheckStep) (ProofCheckReport, error) {
	_, native := PresubmitExecutionFromContext(ctx)
	for _, step := range steps {
		if native && ctx.Err() != nil {
			report.Status = "failed"
			return report, context.Cause(ctx)
		}
		if err := PresubmitGateStarted(ctx, step, &report); err != nil {
			report.Status = "failed"
			return report, err
		}
		result := step
		if report.DryRun {
			result.Status = "planned"
		} else {
			result = run(ctx, step)
			if result.Status == "failed" {
				report.Status = "failed"
			}
		}
		if result.Status == "ok" || result.Status == "planned" {
			report.ProofRefs = append(report.ProofRefs, result.ProofRef)
		}
		report.Steps = append(report.Steps, result)
		if err := PresubmitGateCompleted(ctx, result, &report); err != nil {
			report.Status = "failed"
			return report, err
		}
	}
	return report, nil
}
