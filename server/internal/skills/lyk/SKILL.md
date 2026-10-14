---
name: lyk
description: Use when the user wants to start research on a question (/ground), see the Lykeion board, read its Inbox, or steer a card. Drives the lyk CLI.
---

# Lykeion board

Lykeion carries research questions across a board: Backlog → Grounding → Hypotheses → Experimentation → Validation → Reporting → Done. Agents do the work on the card owner's machine; gates move cards. People steer: they add questions, comment, send cards back, stop them, change budgets and answer escalations. Nobody approves a gate.

## Start research (`/ground <question>`)

    lyk card new --usd 20 "<the full question>"

WIP pull starts it when the workspace has room. If an active card already asks the same question, `lyk card new` refuses and names it; add `--follows-from N` only when the new card continues that one. Keep `lyk daemon` running on your machine: your cards only run on your runtimes.

## Look

    lyk board          # columns, with a Blocked row for cards that need a person
    lyk card show N    # a card, its comments and gates
    lyk inbox          # escalations, out-of-budget cards, releases

## Steer

    lyk card comment N "<context for the next round>"
    lyk card send-back N --to grounding --reason "…"   # the reason reaches the redone role
    lyk card stop N --reason "…"
    lyk card stop N --duplicate-of M                 # ends N as "duplicate of #M"
    lyk card budget N --usd 40
    lyk card resume N
