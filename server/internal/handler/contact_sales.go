package handler

import (
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"unicode"

	"lykeion/server/internal/auth"
)

type contactSalesInput struct {
	FirstName         string `json:"first_name"`
	LastName          string `json:"last_name"`
	Email             string `json:"email"`
	Company           string `json:"company"`
	CompanySize       string `json:"company_size"`
	Country           string `json:"country"`
	Purpose           string `json:"purpose"`
	Goals             string `json:"goals"`
	ContactConsent    bool   `json:"contact_consent"`
	MarketingConsent  bool   `json:"marketing_consent"`
	ProcessingConsent bool   `json:"processing_consent"`
}

func contactField(s string, max int, required, multiline bool) bool {
	if len(s) > max || (required && strings.TrimSpace(s) == "") {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) && !(multiline && (c == '\n' || c == '\r' || c == '\t')) {
			return false
		}
	}
	return true
}

func businessEmail(value string) bool {
	if !contactField(value, 254, true, false) {
		return false
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || address.Name != "" {
		return false
	}
	_, domain, ok := strings.Cut(value, "@")
	if !ok || !strings.Contains(domain, ".") {
		return false
	}
	switch strings.ToLower(domain) {
	case "gmail.com", "googlemail.com", "outlook.com", "hotmail.com", "live.com", "msn.com", "yahoo.com", "yahoo.co.uk", "icloud.com", "me.com", "mac.com", "aol.com", "proton.me", "protonmail.com", "pm.me", "mail.com", "gmx.com", "gmx.net":
		return false
	}
	return true
}

func (h *H) contactSales(w http.ResponseWriter, r *http.Request) {
	var in contactSalesInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "Invalid contact request")
		return
	}
	in.Email = normEmail(in.Email)
	if !contactField(in.FirstName, 120, true, false) || !contactField(in.LastName, 120, true, false) ||
		!businessEmail(in.Email) || !contactField(in.Company, 120, true, false) ||
		!contactField(in.CompanySize, 100, true, false) || !contactField(in.Country, 100, true, false) ||
		!contactField(in.Purpose, 100, true, false) || !contactField(in.Goals, 4000, false, true) || !in.ProcessingConsent {
		writeErr(w, 400, "Please complete the required fields with a business email and consent to processing")
		return
	}
	if h.limited(w, r, map[string]int{"contact:ip:" + h.clientIP(r): 10}) ||
		h.limited(w, r, map[string]int{"contact:global": 500}) {
		return
	}
	if h.Mail == nil {
		writeErr(w, 503, "Contact service unavailable")
		return
	}
	switch h.Mail.(type) {
	case auth.LogMailer, *auth.LogMailer:
		writeErr(w, 503, "Contact service unavailable")
		return
	}
	body := fmt.Sprintf("Name: %s %s\nEmail: %s\nCompany: %s\nCompany size: %s\nCountry / region: %s\nPurpose: %s\nGoals or challenges: %s\nContact consent: %t\nMarketing consent: %t\nProcessing consent: %t", in.FirstName, in.LastName, in.Email, in.Company, in.CompanySize, in.Country, in.Purpose, in.Goals, in.ContactConsent, in.MarketingConsent, in.ProcessingConsent)
	if err := h.Mail.Send(r.Context(), "noreply@lykeion.tech", "Lykeion contact sales request", body); err != nil {
		writeErr(w, 503, "Contact service unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
