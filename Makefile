canary:
	cd server && LYK_CANARY=1 go test ./internal/daemon -run TestCanary -count=1 -v

.PHONY: canary
