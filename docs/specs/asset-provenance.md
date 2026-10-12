# Frontend reference and asset provenance

The user authorized reuse of Lykeion brand artwork while requiring an independent frontend implementation. The reference is the rendered UI of `/Applications/Lykeion.app`, observed through native accessibility/screenshots and Rea's passive Electron inventory. Application script sources were excluded from the Rea capture. Reference component source was not used as the implementation template.

| Material | Source | Use |
| --- | --- | --- |
| Aristotle idle animation | User's `/Users/diegomarono/lykeion/packages/ui/assets/aristotle/idle.json` | Copied brand asset at `apps/dashboard/public/brand/aristotle-idle.json`; independently rendered with lottie-web |
| Inter variable font | `@fontsource-variable/inter` npm package | Shared typography |
| Emoji names, categories and skin variants | `emojibase-data@17.0.0` npm package (MIT) | Independently written picker; dataset generated with `scripts/generate-emoji-data.mjs`, loaded on demand |
| UI icons | `@lucide/vue` npm package | Independently selected icon library |
| Desktop shell | Electron npm package | Independent wrapper around the shared renderer |

Aristotle asset SHA-256: `4c36a74791672b420c62f06a2772a9ff9115de01c227c013e16799b4bf5bbf08`.

Reference labels, geometry, interaction observations and screenshots form an acceptance inventory. Existing workspace records, agent instructions, credentials and source-code implementation are not copied into product defaults. Third-party packages retain their own licenses; this provenance record is not a legal determination about visual similarity or distribution rights.

The launcher renders the original authorized idle animation in a bust viewport, showing the full head plus a small portion of the shoulders and robe, as requested. The full-body presentation uses the same unchanged asset. The head-only derivative is retained as an unused historical variant.
