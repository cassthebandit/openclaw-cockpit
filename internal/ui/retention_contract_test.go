package ui

import (
	"encoding/json"
	"os/exec"
	"testing"
)

func TestPythonRetentionProducerUIContract(t *testing.T) {
	// Execute the real serializer and apply classifier, not a handwritten vocabulary.
	source := `import sys,json,argparse
sys.path.insert(0,'../..')
from helpers.tmux import session_hygiene as h
pane=h.Pane(session='worker',window='0',pane='%1',title='',command='claude',path='/tmp',created='1',last_activity='1',dead=False,dead_status='',meta={},pid='100',server_session_id='$1',window_linked='0',session_grouped='0')
reasons=['explicit_keep_open',h.dead_retirement_refusal([pane]),h.dead_retirement_refusal([pane,pane])]
pane.window_linked='1'
reasons.append(h.dead_retirement_refusal([pane]))
items=[h.apply_refusal({'session':str(i)},reason) for i,reason in enumerate(reasons)]
print(json.dumps(h.status_payload(items,argparse.Namespace())))`
	out, err := exec.Command("python3", "-c", source).CombinedOutput()
	if err != nil {
		t.Fatalf("Python producer: %v: %s", err, out)
	}
	var payload janitorStatusFile
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0", "1", "2", "3"} {
		session := agentSessionForGroup("worker", "done")
		row := sidecarRowFor(session, payload.Sessions[name])
		m := modelWithJanitorSidecar(map[string]janitorSessionStatus{"worker": row})
		got := agentLifecycleGroup(m, session, "done")
		if name == "0" || name == "1" {
			if got.name != groupCompletedAgents.name {
				t.Fatalf("retention %s grouped %s, row %+v", name, got.name, row)
			}
		} else if row.JanitorState != "cleanup_blocked" || got.name == groupCompletedAgents.name {
			t.Fatalf("unsafe topology hidden: %s %+v %s", name, row, got.name)
		}
	}
}
