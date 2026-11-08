package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type chartStudy struct {
	ID    uuid.UUID `json:"id"`
	Title string    `json:"title"`
}

type chartAgent struct {
	ID        uuid.UUID    `json:"id"`
	Name      string       `json:"name"`
	Emoji     string       `json:"emoji"`
	Role      string       `json:"role"`
	Source    string       `json:"source"`
	Status    string       `json:"status"`
	Since     *time.Time   `json:"since"`
	Studies   []chartStudy `json:"studies"`
	Machine   *string      `json:"machine"`
	CLI       *string      `json:"cli"`
	ReportsTo string       `json:"reports_to"`
}

type chartRoom struct {
	Stage  string       `json:"stage"`
	Team   string       `json:"team"`
	Agents []chartAgent `json:"agents"`
}

type chartPerson struct {
	UserID        uuid.UUID `json:"user_id"`
	Name          string    `json:"name"`
	Role          string    `json:"role"`
	OpenDecisions int64     `json:"open_decisions"`
}

type chartTicker struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

type labChartView struct {
	Rooms  []chartRoom   `json:"rooms"`
	People []chartPerson `json:"people"`
	Ticker *chartTicker  `json:"ticker"`
}

func stageTitle(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

// labChart is the Lab Chart's live state as the viewer sees it: every stage's
// agents with what each is doing, the workspace's people, and the newest event.
func (h *H) labChart(w http.ResponseWriter, r *http.Request) {
	out := labChartView{Rooms: []chartRoom{}, People: []chartPerson{}}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		ctx, admin := r.Context(), agentAdmin(r)
		agents, err := tx.Q.ListAgents(ctx, db.ListAgentsParams{WorkspaceID: ws, UserID: user, IsAdmin: admin})
		if err != nil {
			return err
		}
		roles, err := tx.Q.LabChartRoles(ctx, ws)
		if err != nil {
			return err
		}
		runtimes, err := tx.Q.LabChartRuntimes(ctx, ws)
		if err != nil {
			return err
		}
		runs, err := tx.Q.LabChartActiveRuns(ctx, ws)
		if err != nil {
			return err
		}
		decisions, err := tx.Q.LabChartDecisions(ctx, db.LabChartDecisionsParams{WorkspaceID: ws, Viewer: user, IsAdmin: admin})
		if err != nil {
			return err
		}
		finished, err := tx.Q.LabChartLastFinished(ctx, ws)
		if err != nil {
			return err
		}
		people, err := tx.Q.LabChartPeople(ctx, ws)
		if err != nil {
			return err
		}
		tick, err := tx.Q.LabChartTicker(ctx, db.LabChartTickerParams{WorkspaceID: ws, Viewer: user, IsAdmin: admin})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		role := map[uuid.UUID]string{}
		for _, x := range roles {
			role[x.AgentID] = x.Member
		}
		machine := map[uuid.UUID]db.LabChartRuntimesRow{}
		for _, x := range runtimes {
			machine[x.ID] = x
		}
		active := map[uuid.UUID][]db.LabChartActiveRunsRow{}
		for _, x := range runs {
			active[*x.AgentID] = append(active[*x.AgentID], x)
		}
		asking := map[uuid.UUID]db.LabChartDecisionsRow{}
		for _, x := range decisions {
			if x.MayAnswer && x.AgentID != uuid.Nil {
				if old, ok := asking[x.AgentID]; !ok || x.CreatedAt.After(old.CreatedAt) {
					asking[x.AgentID] = x
				}
			}
		}
		last := map[uuid.UUID]time.Time{}
		for _, x := range finished {
			last[*x.AgentID] = x.At
		}

		byStage := map[string][]chartAgent{}
		for _, a := range agents {
			if a.ArchivedAt != nil || board.StageIndex(a.Stage) < 0 {
				continue
			}
			emoji, _ := strings.CutPrefix(a.AvatarUrl, "emoji:")
			c := chartAgent{ID: a.ID, Name: a.Name, Emoji: emoji, Source: "researcher", Role: "member", Status: "idle", Studies: []chartStudy{},
				ReportsTo: stageTitle(a.Stage) + " leader"}
			if a.Laboratory != "" {
				c.Source = "laboratory"
				if m := role[a.ID]; m == "leader" || m == "auditor" {
					c.Role = m
				}
			}
			switch c.Role {
			case "leader":
				c.ReportsTo = "the researchers"
			case "auditor":
				c.ReportsTo = "nobody: checks " + stageTitle(a.Stage) + " independently"
			}
			rt := a.RuntimeID
			if rs := active[a.ID]; len(rs) > 0 {
				c.Status, c.Since = "working", &rs[0].Since
				for _, x := range rs {
					c.Studies = append(c.Studies, chartStudy{ID: *x.CardID, Title: x.Title})
				}
				if rs[0].RuntimeID != nil {
					rt = rs[0].RuntimeID
				}
			} else if t, ok := last[a.ID]; ok {
				c.Since = &t
			}
			if d, ok := asking[a.ID]; ok {
				c.Status, c.Since = "needs_you", &d.CreatedAt
			}
			if rt != nil {
				if m, ok := machine[*rt]; ok {
					c.Machine, c.CLI = &m.Machine, &m.Harness
				}
			}
			byStage[a.Stage] = append(byStage[a.Stage], c)
		}
		for _, s := range board.Stages {
			out.Rooms = append(out.Rooms, chartRoom{Stage: s, Team: stageTitle(s) + " Team", Agents: append([]chartAgent{}, byStage[s]...)})
		}
		for _, p := range people {
			out.People = append(out.People, chartPerson{UserID: p.UserID, Name: p.Name, Role: p.Role, OpenDecisions: p.OpenDecisions})
		}
		if tick.Event != "" {
			stage := stageTitle(tick.Stage)
			text := tick.AgentName + " is waiting for your answer on “" + tick.Title + "”"
			if tick.Event != "waiting" {
				text = fmt.Sprintf("%s %s %s on “%s”", tick.AgentName, tick.Event, stage, tick.Title)
			}
			out.Ticker = &chartTicker{At: *tick.At, Text: text}
		}
		return nil
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
