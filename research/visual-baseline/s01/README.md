# S01 Colleagues acceptance evidence

The installed `/Applications/Lykeion.app` was inspected as a visual and interaction reference only. Its accounts and data were not modified. The independently implemented preview uses the original Colleagues name, route, introduction and compact list geometry.

## Evidence

- `reference-colleagues.png`: installed desktop reference at 1920 × 960.
- `rebuild-colleagues.png` and `.ax.txt`: connected desktop with synthetic owner and viewer, accepted invitation and structured access decision.
- `web-viewer-colleagues.png`: recipient web view after access changed to viewer.
- `web-after-leave.png`: user-approved disposable leave completed; the remaining personal test workspace is selected.
- `desktop-after-leave.png`: owner view after departure, showing only the owner.
- General/settings and switcher reference captures document navigation. Earlier `rebuild-members` captures record the superseded settings placement, not the final acceptance layout.

Membership administration adds invitation, access-decision and leave controls beneath the original compact list. These are intentional extensions, so the complete page is not claimed pixel-identical. Account names and roles differ from the reference by design. No reference source code was used for these features.

## Fixture and observed interactions

Acceptance used schema `ui_bf59d5a692a97ab8` in the isolated local development database, with S01 Test Scientist and S01 Test Colleague only and zero research programmes. Invitation creation, recipient-bound acceptance, cross-client membership refresh, structured viewer approval and approved leave were exercised through the UI. Proofs and login codes are private, ignored files and are not included in this evidence.

The native owner was reconnected after a development hot reload before checking the departure. Therefore the final owner capture proves the current projection, not uninterrupted WebSocket delivery for that particular leave. Earlier acceptance observed cross-client membership updates. The main-process session is currently memory-only; persistent credentials and reconnect acceptance remain pending S01 work.

The fixture is retained for continued local acceptance. Do not point the acceptance launcher at the installed reference application's database.
