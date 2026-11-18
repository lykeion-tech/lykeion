# Contributing to Lykeion

Thanks for helping. Bug reports, feature ideas and pull requests are all welcome.

## Issues

- **Bugs:** open a [bug report](https://github.com/lykeion-tech/lykeion/issues/new?template=bug_report.yml) with steps to reproduce and your version.
- **Features:** open a [feature request](https://github.com/lykeion-tech/lykeion/issues/new?template=feature_request.yml) that starts from the problem you have.
- **Questions:** ask on [Discord](https://discord.gg/ed3jsMTs3) or read the [docs](https://app.lykeion.tech/docs/).

New reports get the `triage` label. When a maintainer confirms a bug they label it `bug`, and Aristotle, the repository's maintenance agent, may pick it up and open a pull request for review.

Never paste tokens, keys or private study content into an issue.

## Pull requests

1. Fork the repository and branch from `main`.
2. Set up the server, dashboard and your machine as described in [Run locally](README.md#run-locally).
3. Keep the change focused, and add or update tests for behaviour that can break.
4. Run the [checks](README.md#checks) that cover what you changed:
   - dashboard and desktop: `npm run typecheck`, `npm run lint`, `npm test`
   - Go server: from `server/`, `GOMAXPROCS=2 go vet ./...` and `GOMAXPROCS=2 go test -p 1 ./... -count=1`
   - website: `npm --prefix apps/web run lint`, `npm --prefix apps/web run typecheck`
5. Open the pull request, and say what changed, why, and which checks you ran.

[`AGENTS.md`](AGENTS.md) describes the architecture and the boundaries every change must keep: workspace isolation, the agent sandbox and evidence provenance.

### Before a pull request can merge

- **CLA:** on your first pull request, CLA assistant comments with a link to the [Contributor License Agreement](CLA.md). The `license/cla` check passes once you sign it.
- **CI:** for first-time contributors, a maintainer starts the CI workflows. The `frontend`, `website`, `go`, `go-macos` and `license/cla` checks must pass.
- **Review:** a maintainer approves the pull request and merges it.

## License

Lykeion is licensed under the [GNU AGPL v3](LICENSE). Contributions are accepted under the CLA.
