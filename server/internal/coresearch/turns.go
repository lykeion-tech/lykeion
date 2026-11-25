package coresearch

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/coresearch/doc"
	"lykeion/server/internal/fence"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var ErrTurnRunning = errors.New("a turn is already running in this chat")

type Service struct {
	Store *store.Store
	Blobs storage.Storage
}

func EnqueueTurn(ctx context.Context, tx *store.Tx, runs *service.AgentRuns, chat db.CoresearchChat, user uuid.UUID, body string) (db.AgentRun, error) {
	return enqueueTurn(ctx, tx, runs, chat, user, body, "", "", false, false, nil)
}

// With queue, a Cowrite turn may wait behind the chat's turn in progress; at
// most one waits at a time, and the claim runs them in order.
func EnqueueAristotleTurn(ctx context.Context, tx *store.Tx, runs *service.AgentRuns, chat db.CoresearchChat, user uuid.UUID, body, instructions, model string, queue, researchDirectory bool, capabilities *Capabilities, selection ...*Selection) (db.AgentRun, error) {
	return enqueueTurn(ctx, tx, runs, chat, user, body, instructions, model, queue, researchDirectory, capabilities, selection...)
}

func enqueueTurn(ctx context.Context, tx *store.Tx, runs *service.AgentRuns, chat db.CoresearchChat, user uuid.UUID, body, instructions, model string, queue, researchDirectory bool, capabilities *Capabilities, selections ...*Selection) (db.AgentRun, error) {
	var zero db.AgentRun
	if user != chat.OwnerID {
		return zero, errors.New("chat owner required")
	}
	if queue && chat.Mode != "cowrite" {
		return zero, doc.ErrInvalid
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
	active, err := tx.Q.CoresearchChatActiveTurns(ctx, db.CoresearchChatActiveTurnsParams{WorkspaceID: tx.WS, ChatID: &chat.ID})
	if err != nil {
		return zero, err
	}
	if active > 1 || (active == 1 && !queue) {
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
	var researcherContext string
	if researchDirectory {
		researcher, e := tx.Q.GetUser(ctx, user)
		if e != nil {
			return zero, e
		}
		researcherContext = fence.Researcher(researcher.Name, user.String())
	}
	prompt := Prompt(PromptIn{ResearchDirectory: researchDirectory, ResearcherContext: researcherContext, Capabilities: capabilities, Selection: selection, ProjectName: p.Name, Files: rows, Mode: chat.Mode, Message: body, History: history, Environment: env, Queued: active > 0})
	if instructions != "" {
		prompt = "Agent instructions:\n" + instructions + "\n\n" + prompt
	}
	message, err := tx.Q.InsertCoresearchMessage(ctx, db.InsertCoresearchMessageParams{WorkspaceID: tx.WS, ChatID: chat.ID, Kind: "turn", AuthorUserID: &user, Body: body})
	if err != nil {
		return zero, err
	}
	r, err := tx.Q.EnqueueCoresearchAgentRun(ctx, db.EnqueueCoresearchAgentRunParams{WorkspaceID: tx.WS, OwnerID: user, Harness: chat.Cli, Prompt: prompt, ContinuityScope: "coresearch:" + chat.ID.String(), CoresearchChatID: &chat.ID})
	if err != nil {
		return zero, err
	}
	if capabilities != nil {
		seen := map[uuid.UUID]bool{}
		for _, id := range capabilities.WorkspaceConnectors {
			if seen[id] {
				return zero, doc.ErrInvalid
			}
			seen[id] = true
			if _, err := tx.Q.GetWorkspaceMCPServer(ctx, db.GetWorkspaceMCPServerParams{WorkspaceID: tx.WS, ID: id}); err != nil {
				return zero, err
			}
		}
		if err := tx.SetCoresearchRunConnectors(ctx, r.ID, capabilities.WorkspaceConnectors); err != nil {
			return zero, err
		}
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
		err := s.Store.Maintenance(ctx, func(tx *store.Tx) error {
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
		if err != nil {
			return err
		}
		var chat db.CoresearchChat
		if n.AgentRun.RequesterID == nil {
			return nil
		}
		err = s.Store.InWorkspace(ctx, n.AgentRun.WorkspaceID, *n.AgentRun.RequesterID, func(tx *store.Tx) error {
			var e error
			chat, e = tx.Q.CoresearchSettlementBuildChat(ctx, db.CoresearchSettlementBuildChatParams{WorkspaceID: tx.WS, ID: n.ID})
			return e
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if s.Blobs == nil {
			return errors.New("Coresearch build storage is unavailable")
		}
		transaction := func(fn func(*store.Tx, uuid.UUID, uuid.UUID) error) error {
			return s.Store.InWorkspace(ctx, chat.WorkspaceID, chat.OwnerID, func(tx *store.Tx) error {
				if _, e := tx.Q.LockWorkspaceForMutation(ctx, tx.WS); e != nil {
					return e
				}
				if _, e := tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: tx.WS, UserID: chat.OwnerID}); e != nil {
					return e
				}
				if _, e := tx.Q.CoresearchMember(ctx, db.CoresearchMemberParams{WorkspaceID: tx.WS, ProjectID: chat.ProjectID, UserID: chat.OwnerID}); e != nil {
					return e
				}
				p, e := tx.Q.LockCoresearchProject(ctx, db.LockCoresearchProjectParams{WorkspaceID: tx.WS, ID: chat.ProjectID})
				if e != nil {
					return e
				}
				if p.ArchivedAt != nil {
					return ErrArchived
				}
				return fn(tx, p.ID, chat.OwnerID)
			})
		}
		_, err = QueueBuild(ctx, s.Store, s.Blobs, chat.WorkspaceID, chat.OwnerID, transaction, n.ID)
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrArchived) {
			return nil
		}
		return err

	}
}
