package handler

import "net/http"

func notYet(w http.ResponseWriter, _ *http.Request) { writeErr(w, 501, "not implemented yet") }

func (h *H) listCards(w http.ResponseWriter, r *http.Request)     { notYet(w, r) }
func (h *H) createCard(w http.ResponseWriter, r *http.Request)    { notYet(w, r) }
func (h *H) getCard(w http.ResponseWriter, r *http.Request)       { notYet(w, r) }
func (h *H) listComments(w http.ResponseWriter, r *http.Request)  { notYet(w, r) }
func (h *H) createComment(w http.ResponseWriter, r *http.Request) { notYet(w, r) }
func (h *H) cardEvents(w http.ResponseWriter, r *http.Request)    { notYet(w, r) }
func (h *H) listInbox(w http.ResponseWriter, r *http.Request)     { notYet(w, r) }
func (h *H) readInbox(w http.ResponseWriter, r *http.Request)     { notYet(w, r) }
