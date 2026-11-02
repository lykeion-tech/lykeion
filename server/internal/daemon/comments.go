package daemon

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lykeion/server/internal/fence"
)

// commentPoll is how often a running turn asks for new comments.
var commentPoll = 5 * time.Second

// commentText is a researcher's comment as the agent reads it: their words
// fenced as data, under a lead-in saying it is guidance for the work rather
// than an item sent back, which a hand-in answers in replies.
func commentText(body string) string {
	return "A researcher commented on the study. Take it into account in your work; it is not a sent-back item, so don't list it in `replies`.\n" +
		fence.Untrusted("The comment", body)
}

// relayComments asks the server every commentPoll for the comments posted on
// the run's study since its attempt started, until ctx ends, and passes each
// to the running turn. The server writes the receipt as it hands one out. A
// failed poll is the machine's or the server's trouble, never the agent's:
// the next tick asks again.
func (d *daemon) relayComments(ctx context.Context, base string, r Report, out chan<- string) {
	q := url.Values{"workspace_id": {r.WorkspaceID.String()}, "runtime_id": {r.RuntimeID.String()}, "attempt": {strconv.Itoa(int(r.Attempt))}}
	t := time.NewTicker(commentPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var got []struct {
			Body string `json:"body"`
		}
		if err := d.api.Do(ctx, "GET", base+"/comments?"+q.Encode(), nil, &got); err != nil {
			continue
		}
		for _, c := range got {
			select {
			case out <- commentText(c.Body):
			case <-ctx.Done():
				return
			}
		}
	}
}

// memberAnswer is the server's member_result reply as the leader reads it:
// the reply without its comments, then each comment as a researcher's words.
func memberAnswer(reply json.RawMessage) *mcp.CallToolResult {
	var m map[string]json.RawMessage
	if json.Unmarshal(reply, &m) != nil {
		return cited("", string(reply))
	}
	var comments []struct {
		Body string `json:"body"`
	}
	json.Unmarshal(m["comments"], &comments)
	delete(m, "comments")
	b, err := json.Marshal(m)
	if err != nil {
		return cited("", string(reply))
	}
	text := string(b)
	for _, c := range comments {
		text += "\n\n" + commentText(c.Body)
	}
	return cited("", text)
}
