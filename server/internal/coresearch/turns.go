package coresearch

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"lykeion/server/internal/coresearch/doc"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var ErrTurnRunning = errors.New("a turn is already running in this chat")

type Service struct{ Store *store.Store }

func EnqueueTurn(ctx context.Context, tx *store.Tx, runs *service.AgentRuns, chat db.CoresearchChat, user uuid.UUID, body string) (db.AgentRun, error) {
	return enqueueTurn(ctx, tx, runs, chat, user, body, "", "")
}

func EnqueueAristotleTurn(ctx context.Context, tx *store.Tx, runs *service.AgentRuns, chat db.CoresearchChat, user uuid.UUID, body, instructions, model string, selection ...*Selection) (db.AgentRun, error) {
	return enqueueTurn(ctx, tx, runs, chat, user, body, instructions, model, selection...)
}

func enqueueTurn(ctx context.Context, tx *store.Tx, runs *service.AgentRuns, chat db.CoresearchChat, user uuid.UUID, body, instructions, model string, selections ...*Selection) (db.AgentRun, error) {
	var zero db.AgentRun
	if user != chat.OwnerID {
		return zero, errors.New("chat owner required")
	}
	var selection *Selection
	if len(selections) > 0 && selections[0] != nil {
		copy := *selections[0]
		selection = &copy
		if chat.Mode != "cowrite" {
			return zero, doc.ErrInvalid
		}
		f, e := tx.Q.GetCoresearchFile(ctx, db.GetCoresearchFileParams{WorkspaceID: tx.WS, ProjectID: chat.ProjectID, ID: selection.FileID})
		if e != nil {
			return zero, e
		}
		d, e := doc.AtVersion(ctx, tx, doc.File{Record: f}, selection.Version)
		if e != nil {
			return zero, e
		}
		if _, e = doc.Slice(d.Text, selection.From, selection.To); e != nil {
			return zero, e
		}
		selection.Path = f.Path
	}
	// The caller holds workspace, project and chat locks before enqueueing.
	busy, err := tx.Q.CoresearchChatTurnBusy(ctx, db.CoresearchChatTurnBusyParams{WorkspaceID: tx.WS, ChatID: &chat.ID})
	if err != nil {
		return zero, err
	}
	if busy {
		return zero, ErrTurnRunning
	}
	p, err := tx.Q.GetCoresearchProject(ctx, db.GetCoresearchProjectParams{WorkspaceID: tx.WS, ID: chat.ProjectID})
	if err != nil {
		return zero, err
	}
	if p.ArchivedAt != nil {
		return zero, ErrArchived
	}
	history, err := tx.Q.CoresearchHistory(ctx, db.CoresearchHistoryParams{WorkspaceID: tx.WS, ChatID: chat.ID})
	if err != nil {
		return zero, err
	}
	files, err := tx.Q.ListCoresearchFiles(ctx, db.ListCoresearchFilesParams{WorkspaceID: tx.WS, ProjectID: chat.ProjectID})
	if err != nil {
		return zero, err
	}
	rows := []FileRow{}
	for _, f := range files {
		head, e := tx.Q.CoresearchFileHead(ctx, db.CoresearchFileHeadParams{WorkspaceID: tx.WS, FileID: f.ID})
		if e != nil {
			return zero, e
		}
		rows = append(rows, FileRow{Path: f.Path, N: head.N, Size: head.Size})
	}
	env := "not set up"
	if p.PythonVersion != nil {
		env = "Python " + *p.PythonVersion
	}
	prompt := Prompt(PromptIn{Selection: selection, ProjectName: p.Name, Files: rows, Mode: chat.Mode, Message: body, History: history, Environment: env})
	if instructions != "" {
		prompt = "Aristotle instructions:\n" + instructions + "\n\n" + prompt
	}
	message, err := tx.Q.InsertCoresearchMessage(ctx, db.InsertCoresearchMessageParams{WorkspaceID: tx.WS, ChatID: chat.ID, Kind: "turn", AuthorUserID: &user, Body: body})
	if err != nil {
		return zero, err
	}
	r, err := tx.Q.EnqueueCoresearchAgentRun(ctx, db.EnqueueCoresearchAgentRunParams{WorkspaceID: tx.WS, OwnerID: user, Harness: chat.Cli, Prompt: prompt, ContinuityScope: "coresearch:" + chat.ID.String(), CoresearchChatID: &chat.ID})
	if err != nil {
		return zero, err
	}
	if model != "" {
		r, err = tx.Q.SetAristotleRunModel(ctx, db.SetAristotleRunModelParams{ID: r.ID, Model: model})
		if err != nil {
			return zero, err
		}
	}
	if err = tx.Q.AttachCoresearchTurn(ctx, db.AttachCoresearchTurnParams{WorkspaceID: tx.WS, ID: message.ID, AgentRunID: &r.ID}); err != nil {
		return zero, err
	}
	if err = tx.Q.TouchCoresearchProject(ctx, db.TouchCoresearchProjectParams{WorkspaceID: tx.WS, ID: chat.ProjectID}); err != nil {
		return zero, err
	}
	if err = tx.Emit(ctx, "coresearch.message.created", nil, store.User(user), map[string]any{"project_id": chat.ProjectID, "chat_id": chat.ID, "message_id": message.ID}); err != nil {
		return zero, err
	}
	return r, tx.Emit(ctx, "agent_run.queued", nil, store.System, map[string]any{"agent_run_id": r.ID, "owner_id": user})
}
func (s *Service) OnSettlement(next func(context.Context, service.Settlement) error) func(context.Context, service.Settlement) error {
	return func(ctx context.Context, n service.Settlement) error {
		if n.AgentRun.Kind != "coresearch" {
			if next != nil {
				return next(ctx, n)
			}
			return nil
		}
		return s.Store.Maintenance(ctx, func(tx *store.Tx) error {
			if err := tx.Workspace(ctx, n.AgentRun.WorkspaceID); err != nil {
				return err
			}
			if _, err := tx.Q.LockWorkspaceExecution(ctx, tx.WS); err != nil {
				return err
			}
			rows, err := tx.Q.SettleCoresearch(ctx, n.ID)
			if err != nil {
				return err
			}
			for _, m := range rows {
				if err = tx.Emit(ctx, "coresearch.message.created", nil, store.System, map[string]any{"project_id": m.ProjectID, "chat_id": m.ChatID, "message_id": m.MessageID}); err != nil {
					return err
				}
			}
			return nil
		})
	}
}
