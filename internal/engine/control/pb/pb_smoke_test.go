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

func TestSessionTypesExist(t *testing.T) {
	_ = &OpenSessionRequest{Profile: "p", Cols: 80, Rows: 24}
	_ = &OpenSessionResponse{SessionId: "s1"}
	_ = &ClientFrame{Msg: &ClientFrame_AttachSessionId{AttachSessionId: "s1"}}
	_ = &ClientFrame{Msg: &ClientFrame_Input{Input: []byte("x")}}
	_ = &ClientFrame{Msg: &ClientFrame_Resize{Resize: &Resize{Cols: 100, Rows: 40}}}
	_ = &ServerFrame{Msg: &ServerFrame_Output{Output: []byte("y")}}
	_ = &ServerFrame{Msg: &ServerFrame_Exited{Exited: &Exited{ExitCode: 0}}}
}
