// Claude Code in a disposable Tart VM — the strongest isolation (heaviest to run).
package safeslop

safeslop: {
	version: 1
	profiles: {
		yolo: {agent: "claude", environment: "vm", network: "allow"}
	}
}
