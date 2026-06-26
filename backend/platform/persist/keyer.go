package persist

import (
	"fmt"

	"github.com/dogring/bdg/engine/kernel/core"
)

// Keyer builds the exact §2 keyspace for a run. The ONLY source of key strings;
// callers never format keys by hand.
//
//	sim:{run}:meta        sim:{run}:tick        sim:{run}:snapshot
//	sim:{run}:agent:{id}  sim:{run}:events
type Keyer struct{ Run core.RunID }

func (k Keyer) Meta() string                 { return fmt.Sprintf("sim:%s:meta", k.Run) }
func (k Keyer) Tick() string                 { return fmt.Sprintf("sim:%s:tick", k.Run) }
func (k Keyer) SnapshotKey() string          { return fmt.Sprintf("sim:%s:snapshot", k.Run) }
func (k Keyer) Agent(id core.AgentID) string { return fmt.Sprintf("sim:%s:agent:%s", k.Run, id) }
func (k Keyer) Events() string               { return fmt.Sprintf("sim:%s:events", k.Run) }
