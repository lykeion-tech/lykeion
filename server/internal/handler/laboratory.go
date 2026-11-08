package handler

import (
	"net/http"

	"lykeion/server/internal/board"
	"lykeion/server/internal/pack"
)

type labAgent struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type labRoom struct {
	Stage  string     `json:"stage"`
	Team   string     `json:"team"`
	Agents []labAgent `json:"agents"`
}

type laboratoryView struct {
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	Rooms []labRoom `json:"rooms"`
}

// listLaboratories describes the built-in laboratory from the shipped stage
// Teams, one room per stage in board order.
func (h *H) listLaboratories(w http.ResponseWriter, r *http.Request) {
	byStage := map[string]labRoom{}
	for _, name := range pack.ShippedNames(h.Shipped) {
		_, p, err := pack.ReadShipped(h.Shipped, name)
		if err != nil {
			fail(w, r, err)
			return
		}
		m := p.Manifest
		room := labRoom{Stage: m.Stage, Team: m.Name, Agents: []labAgent{{m.Leader.Name, "leader"}, {m.Auditor.Name, "auditor"}}}
		for _, mem := range m.Members {
			room.Agents = append(room.Agents, labAgent{mem.Name, "member"})
		}
		byStage[m.Stage] = room
	}
	rooms := []labRoom{}
	for _, s := range board.Stages {
		if room, ok := byStage[s]; ok {
			rooms = append(rooms, room)
		}
	}
	writeJSON(w, 200, []laboratoryView{{ID: "built-in", Name: "Built-in laboratory", Rooms: rooms}})
}
