# Independent dashboard implementation

> Fresh backend design: [API contracts](backend-api.md) and [implementation plan](../plans/backend-implementation.md) supersede the provisional transport below. Existing renderer implementation descriptions remain historical/current-code context, not backend authority.

> Implementation note: [Lykeion product and scientific workflow specification](scientific-workflow.md) v0.2 is authoritative for agreed behavior. The contracts and structures below describe the existing provisional frontend and must be reconciled with that specification before backend wiring.
The reference is observed at runtime. Upstream components, application stylesheets, data stores, are not implementation inputs. The user-approved Aristotle brand animation is reused as an asset; its provenance is recorded separately. The authenticated dashboard, including its desktop shell, is the target; the marketing website is excluded.

Vue 3 and TypeScript provide the shared dashboard renderer. Vite builds static assets suitable for both browsers and Electron. Vue Router owns navigation; application-owned CSS expresses measured geometry and visual tokens. Lucide's Vue distribution is acquired independently for standard icons. Electron supplies the desktop host with a sandboxed, isolated renderer and a narrow preload bridge. These choices are independent of upstream module boundaries.

The renderer depends on a typed ResearchService port. HTTP is the production integration boundary for the Go control plane. Python scientific execution remains behind that control plane; browser code never drives Python processes or decides scientific gates. A local workspace adapter supports empty first-install exploration and explicitly local drafts while the Go API is being built. It cannot claim that experiments ran or that a scientific transition passed.

Resource loading, errors, empty lists, and mutation failures are explicit. Scientific stages and operational state remain separate. A transition request is a request to the backend, not permission for the browser to accept a claim or advance a stage. The frontend presents Jev's routing receipt and the control plane's enforcement result when supplied.

Sources for selected platform behavior: [Vue TypeScript](https://vuejs.org/guide/typescript/overview) and [Electron security](https://www.electronjs.org/docs/latest/tutorial/security). Visual acceptance is tracked in the runtime capture inventory, independently of compilation and interaction tests.


## Shared desktop and web surfaces

`App.vue` composes the sidebar, global menus, search and stage/resource views. `WorkspaceTabs.vue` owns independent per-tab navigation histories, pin/close/reorder and session persistence. Both renderer targets use this implementation. The Electron host only supplies native window behavior and a narrow documentation-opening bridge, with a separate preview profile.

Task views provide Board, List, Table and Swimlane layouts. Table edits use the same revision-aware service as task details; status changes still go through transition admission. The task inspector owns Files, Notebook and Compute tabs and cannot manufacture artifacts or kernel state. Agent, runtime, connector, skill and automation forms keep configuration drafts distinct from activated resources.

Component boundaries follow these independently chosen responsibilities, not the reference application's source layout. Native screenshots and accessibility states are acceptance inputs. Captured user records and transcripts are never bundled as seed data.

## Verification boundary

Compile, service tests and isolated browser interaction tests establish only the behavior they exercise. A feature can be implemented and tested while still requiring visual adjustment. The capture coverage matrix explicitly distinguishes observed reference states, implemented surfaces, backend capability gaps and remaining visual acceptance.
