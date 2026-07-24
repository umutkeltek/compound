package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"compound/internal/work"
)

func runWorkCollaborate(store *work.Store, args []string, now time.Time) {
	usage := workUsage("collaborate")
	var (
		workID          string
		actor           string
		idempotencyKey  string
		sessionID       string
		runAttemptID    string
		targetActor     string
		bodyRef         string
		visibility      string
		causalParentIDs []string
		jsonOutput      bool
	)
	value := func(name string, i *int) string {
		if *i+1 >= len(args) || strings.TrimSpace(args[*i+1]) == "" {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		*i++
		return strings.TrimSpace(args[*i])
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--actor":
			actor = value("--actor", &i)
		case "--idempotency-key":
			idempotencyKey = value("--idempotency-key", &i)
		case "--session":
			sessionID = value("--session", &i)
		case "--run-attempt":
			runAttemptID = value("--run-attempt", &i)
		case "--target-actor":
			targetActor = value("--target-actor", &i)
		case "--causal-parent":
			causalParentIDs = append(causalParentIDs, value("--causal-parent", &i))
		case "--body-ref":
			bodyRef = value("--body-ref", &i)
		case "--visibility":
			visibility = value("--visibility", &i)
		case "--json":
			jsonOutput = true
		default:
			if strings.HasPrefix(args[i], "-") || workID != "" {
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
			workID = strings.TrimSpace(args[i])
		}
	}
	if workID == "" || actor == "" || idempotencyKey == "" || bodyRef == "" {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	result, err := store.RecordCollaboration(workID, work.CollaborationInput{
		Actor:           actor,
		IdempotencyKey:  idempotencyKey,
		SessionID:       sessionID,
		RunAttemptID:    runAttemptID,
		TargetActor:     targetActor,
		CausalParentIDs: causalParentIDs,
		BodyRef:         bodyRef,
		Visibility:      visibility,
	}, now)
	die(err)
	if jsonOutput {
		printJSON(result)
		return
	}
	fmt.Printf("%s collaboration=%s actor=%s created=%v body_ref=%s\n", result.Work.ID, result.Event.ID, result.Event.Actor, result.Created, result.Event.BodyRef)
}
