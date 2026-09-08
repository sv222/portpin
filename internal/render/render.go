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

// Table writes an aligned, human-readable listing of bindings. Rows that would
// print identically are shown once, with SOCKETS holding how many sockets stand
// behind them: one process can legitimately hold several distinct sockets on the
// same endpoint - SO_REUSEADDR mDNS listeners open one per interface - and these
// columns cannot tell those sockets apart. Only the table folds them; the JSON
// report still lists every socket.
func Table(w io.Writer, bs []model.Binding) error {
	if len(bs) == 0 {
		_, err := fmt.Fprintln(w, "no matching endpoints")
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "PROTO\tENDPOINT\tSTATE\tPID\tUSER\tPROCESS\tSOCKETS"); err != nil {
		return err
	}
	for _, r := range foldRows(bs) {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\n",
			r.proto, r.endpoint, r.state, r.pid, r.user, r.name, r.sockets); err != nil {
			return err
		}
	}
	return tw.Flush()
}

type rowKey struct {
	proto    string
	endpoint string
	state    string
	pid      string
	user     string
	name     string
}

type tableRow struct {
	rowKey
	sockets int
}

// foldRows groups bindings by their rendered cells, keeping first-appearance
// order so the listing stays stable between runs.
func foldRows(bs []model.Binding) []tableRow {
	rows := make([]tableRow, 0, len(bs))
	at := make(map[rowKey]int, len(bs))
	for _, b := range bs {
		k := rowKey{
			proto:    b.Protocol.String(),
			endpoint: b.Endpoint.String(),
			state:    b.State.String(),
			pid:      "-",
			user:     "-",
			name:     "-",
		}
		if b.Proc != nil {
			k.pid = strconv.FormatUint(uint64(b.Proc.PID), 10)
			if b.Proc.User != "" {
				k.user = b.Proc.User
			}
			if b.Proc.Name != "" {
				k.name = b.Proc.Name
			}
		}
		if i, ok := at[k]; ok {
			rows[i].sockets++
			continue
		}
		at[k] = len(rows)
		rows = append(rows, tableRow{rowKey: k, sockets: 1})
	}
	return rows
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
