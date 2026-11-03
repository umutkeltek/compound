package main

import "compound/internal/hook"

// The live input translator graduated to internal/hook unchanged. These
// aliases keep the CLI-level tests that pin its behavior running in place.
var (
	liveActionIntents          = hook.LiveActionIntents
	liveActionIntentsForHome   = hook.LiveActionIntentsForHome
	liveIngressSourcesForHome  = hook.LiveIngressSourcesForHome
	liveInternalDataSinks      = hook.LiveInternalDataSinks
	liveMonitoredAction        = hook.LiveMonitoredAction
	liveMutationInputReference = hook.LiveMutationInputReference
	liveToolPaths              = hook.LiveToolPaths
)
