package auth

type Google struct{}

func (Google) Enabled() bool { return false }
