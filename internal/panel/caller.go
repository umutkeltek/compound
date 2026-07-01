package panel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CallRequest struct {
	LensID    string  `json:"lens_id"`
	TargetRef string  `json:"target_ref,omitempty"`
	Lane      LaneRef `json:"lane"`
	System    string  `json:"system"`
	User      string  `json:"user"`
}

type CallResult struct {
	Content     []byte `json:"content"`
	TotalTokens int    `json:"total_tokens"`
	LatencyMS   int64  `json:"latency_ms"`
}

type LensCaller interface {
	Call(ctx context.Context, req CallRequest) (CallResult, error)
}

type FixtureCaller struct {
	Dir string
}

func (f FixtureCaller) Call(ctx context.Context, req CallRequest) (CallResult, error) {
	if err := ctx.Err(); err != nil {
		return CallResult{}, err
	}
	if strings.TrimSpace(req.LensID) == "" {
		return CallResult{}, fmt.Errorf("lens id required")
	}
	targetRef := fixtureTargetRef(req)
	if targetRef == "" {
		return CallResult{}, fmt.Errorf("target ref required")
	}
	digest := targetDigest(targetRef)
	var path string
	if req.LensID == "judge" {
		path = filepath.Join(f.Dir, "judge-responses", digest+".json")
	} else {
		path = filepath.Join(f.Dir, "lens-responses", req.LensID, digest+".json")
	}
	start := time.Now()
	data, err := os.ReadFile(path)
	if err != nil {
		return CallResult{}, fmt.Errorf("read fixture response %s: %w", path, err)
	}
	return CallResult{Content: data, TotalTokens: tokenCount(req.System) + tokenCount(req.User) + tokenCount(string(data)), LatencyMS: time.Since(start).Milliseconds()}, nil
}

func fixtureTargetRef(req CallRequest) string {
	if strings.TrimSpace(req.TargetRef) != "" {
		return strings.TrimSpace(req.TargetRef)
	}
	var obj struct {
		TargetRef string `json:"target_ref"`
		Ref       string `json:"ref"`
	}
	if json.Unmarshal([]byte(req.User), &obj) == nil {
		if strings.TrimSpace(obj.TargetRef) != "" {
			return strings.TrimSpace(obj.TargetRef)
		}
		if strings.TrimSpace(obj.Ref) != "" {
			return strings.TrimSpace(obj.Ref)
		}
	}
	for _, line := range strings.Split(req.User, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		for _, prefix := range []string{"target_ref:", "ref:"} {
			if strings.HasPrefix(lower, prefix) {
				return strings.TrimSpace(line[len(prefix):])
			}
		}
	}
	if user := strings.TrimSpace(req.User); user != "" && !strings.ContainsAny(user, " \n\t{}:") {
		return user
	}
	return ""
}

func targetDigest(ref string) string {
	sum := sha256.Sum256([]byte(ref))
	return hex.EncodeToString(sum[:])[:12]
}

func tokenCount(s string) int {
	return len(strings.Fields(s))
}
