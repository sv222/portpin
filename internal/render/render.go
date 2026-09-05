// Package render turns discovery and termination results into the two output
// formats portpin supports: an aligned human table and a stable JSON object.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"

	"github.com/sv222/portpin/internal/advise"
	"github.com/sv222/portpin/internal/model"
)

// Table writes an aligned, human-readable listing of bindings.
func Table(w io.Writer, bs []model.Binding) error {
	if len(bs) == 0 {
		_, err := fmt.Fprintln(w, "no matching endpoints")
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "PROTO\tENDPOINT\tSTATE\tPID\tUSER\tPROCESS"); err != nil {
		return err
	}
	for _, b := range bs {
		pid, user, name := "-", "-", "-"
		if b.Proc != nil {
			pid = strconv.FormatUint(uint64(b.Proc.PID), 10)
			if b.Proc.User != "" {
				user = b.Proc.User
			}
			if b.Proc.Name != "" {
				name = b.Proc.Name
			}
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			b.Protocol, b.Endpoint, b.State, pid, user, name); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// Action is what portpin did to one process.
type Action struct {
	PID          uint32           `json:"pid"`
	Outcome      string           `json:"outcome"`
	GracefulSent bool             `json:"graceful_sent"`
	HardKill     bool             `json:"hard_kill"`
	Error        string           `json:"error,omitempty"`
	Advisory     *advise.Advisory `json:"advisory,omitempty"`
}

// Report is the complete JSON document. Its shape is stable from v0.1: fields
// may be added, but existing fields are never removed or retyped.
type Report struct {
	Target   string          `json:"target"`
	Bindings []model.Binding `json:"bindings"`
	Actions  []Action        `json:"actions"`
	ExitCode int             `json:"exit_code"`
}

// JSON writes r as a single indented JSON object followed by a newline.
func JSON(w io.Writer, r Report) error {
	if r.Bindings == nil {
		r.Bindings = []model.Binding{}
	}
	if r.Actions == nil {
		r.Actions = []Action{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
