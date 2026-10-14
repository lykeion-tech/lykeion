package handler

import "net/http"

func notYet(w http.ResponseWriter, _ *http.Request) { writeErr(w, 501, "not implemented yet") }

func (h *H) googleStart(w http.ResponseWriter, r *http.Request)      { notYet(w, r) }
func (h *H) googleCallback(w http.ResponseWriter, r *http.Request)   { notYet(w, r) }
func (h *H) listWorkspaces(w http.ResponseWriter, r *http.Request)   { notYet(w, r) }
func (h *H) createWorkspace(w http.ResponseWriter, r *http.Request)  { notYet(w, r) }
func (h *H) acceptInvitation(w http.ResponseWriter, r *http.Request) { notYet(w, r) }
func (h *H) getWorkspace(w http.ResponseWriter, r *http.Request)     { notYet(w, r) }
func (h *H) renameWorkspace(w http.ResponseWriter, r *http.Request)  { notYet(w, r) }
func (h *H) deleteWorkspace(w http.ResponseWriter, r *http.Request)  { notYet(w, r) }
func (h *H) listMembers(w http.ResponseWriter, r *http.Request)      { notYet(w, r) }
func (h *H) setMemberRole(w http.ResponseWriter, r *http.Request)    { notYet(w, r) }
func (h *H) removeMember(w http.ResponseWriter, r *http.Request)     { notYet(w, r) }
func (h *H) listInvitations(w http.ResponseWriter, r *http.Request)  { notYet(w, r) }
func (h *H) createInvitation(w http.ResponseWriter, r *http.Request) { notYet(w, r) }
func (h *H) revokeInvitation(w http.ResponseWriter, r *http.Request) { notYet(w, r) }
func (h *H) leave(w http.ResponseWriter, r *http.Request)            { notYet(w, r) }
func (h *H) listCards(w http.ResponseWriter, r *http.Request)        { notYet(w, r) }
func (h *H) createCard(w http.ResponseWriter, r *http.Request)       { notYet(w, r) }
func (h *H) getCard(w http.ResponseWriter, r *http.Request)          { notYet(w, r) }
func (h *H) listComments(w http.ResponseWriter, r *http.Request)     { notYet(w, r) }
func (h *H) createComment(w http.ResponseWriter, r *http.Request)    { notYet(w, r) }
func (h *H) cardEvents(w http.ResponseWriter, r *http.Request)       { notYet(w, r) }
func (h *H) listInbox(w http.ResponseWriter, r *http.Request)        { notYet(w, r) }
func (h *H) readInbox(w http.ResponseWriter, r *http.Request)        { notYet(w, r) }
