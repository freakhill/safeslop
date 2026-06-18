package pb

import "testing"

func TestGeneratedTypesExist(t *testing.T) {
	_ = &PingResponse{Version: "x"}
	_ = &LaunchEvent{Kind: LaunchEvent_SPAWNED, ExitCode: 0}
	_ = &ListProfilesResponse{Profiles: []*Profile{{Name: "p"}}}
	if LaunchEvent_EXITED == LaunchEvent_SPAWNED {
		t.Fatal("enum values collapsed")
	}
}
