package work

import (
	"fmt"
	"path/filepath"
	"strings"

	"compound/internal/authority/gitrepository"
)

func readRetirementDeliverySnapshot(request WorktreeRetirementDeliveryCaptureRequest) (retirementDeliverySnapshot, error) {
	out := retirementDeliverySnapshot{Bodies: map[string][]byte{}}
	read := func(path string) ([]byte, error) { return readDeliveryCaptureFile(path, privateDeliveryMetadataLimit) }
	planBody, err := read(request.PlanPath)
	if err != nil {
		return out, err
	}
	authorizationBody, err := read(request.ReviewReportPath)
	if err != nil {
		return out, err
	}
	var authorization gitrepository.WorktreeRetirementReviewReport
	if err := decodeDeliveryCapture(authorizationBody, &authorization); err != nil {
		return out, err
	}
	if filepath.Clean(request.PlanPath) != filepath.Clean(authorization.PlanPath) || filepath.Clean(request.ReviewReportPath) != filepath.Clean(authorization.ReviewPath) {
		return out, fmt.Errorf("capture paths differ from original retirement authority")
	}
	sourceBody, err := read(authorization.SourcePath)
	if err != nil {
		return out, err
	}
	resultBody, err := read(request.ResultPath)
	if err != nil {
		return out, err
	}
	out.Delivery, err = gitrepository.ValidateRetirementDelivery(planBody, authorizationBody, sourceBody, resultBody)
	if err != nil {
		return out, err
	}
	if filepath.Clean(request.ResultPath) != filepath.Clean(out.Delivery.Result.ResultPath) {
		return out, fmt.Errorf("capture result path differs from original result")
	}
	out.Evidence = retirementDeliveryEvidence{SchemaVersion: retirementDeliveryEvidenceSchema,
		WorkID: out.Delivery.Plan.OwnerProof.WorkID, ActionIntentID: out.Delivery.Result.ActionIntentID,
		PlanRef: privateDeliveryArtifactRef(planBody), AuthorizationRef: privateDeliveryArtifactRef(authorizationBody),
		ReviewedSourceRef: privateDeliveryArtifactRef(sourceBody), ResultRef: privateDeliveryArtifactRef(resultBody)}
	for _, body := range [][]byte{planBody, authorizationBody, sourceBody, resultBody} {
		out.Bodies[privateDeliveryArtifactRef(body)] = body
	}
	proofs := out.Delivery.Plan.RecoveryProofs
	if len(proofs) == 0 || len(proofs) > maxPrivateRuntimeDeliveryFiles || len(proofs) != len(out.Delivery.Plan.RecoveryRefs) {
		return out, fmt.Errorf("retirement recovery inventory out of bounds")
	}
	remaining := int64(privateDeliveryCaptureTotalBytes)
	for i, proof := range proofs {
		if proof.Kind != "sha256-file" || proof.Reference != out.Delivery.Plan.RecoveryRefs[i] ||
			!gitrepository.ValidRetirementDigest(proof.Digest) || !filepath.IsAbs(proof.Path) || filepath.Clean(proof.Path) != proof.Path ||
			proof.Reference != "sha256-file:"+strings.TrimPrefix(proof.Digest, "sha256:")+":"+proof.Path {
			return out, fmt.Errorf("retirement recovery proof is not an exact supported file")
		}
		body, err := readDeliveryCaptureFile(proof.Path, min(remaining, privateDeliveryBodyLimit))
		if err != nil {
			return out, err
		}
		remaining -= int64(len(body))
		ref := privateDeliveryArtifactRef(body)
		if ref != "artifact:"+proof.Digest {
			return out, fmt.Errorf("retirement recovery bytes differ from sealed proof")
		}
		out.Bodies[ref] = body
		out.Evidence.RecoveryArtifacts = append(out.Evidence.RecoveryArtifacts, ref)
	}
	return out, nil
}
