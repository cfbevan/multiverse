# Multiverse Coding Agent Instructions

## Core Behavior

- Make focused, minimal changes that solve the reported issue end-to-end. Do not leave not implemented error returns.
- Prefer fixing root causes over adding workarounds.
- Keep existing style and architecture unless change is required.

## Lint and Quality Rules

- Do not add nolint directives.
- If lint fails, update the code so it passes cleanly without suppression.
- Avoid TODO/BUG/FIXME comments that fail the configured linters.

## Backend Conventions

- For request body limits, enforce explicit bounds with `http.MaxBytesReader` before parsing forms.
- Validate all user inputs and return structured errors for HTMX fragments and JSON APIs.
- Preserve authentication checks and section gating in handlers.

## HTMX and UI Conventions

- HTMX create forms must send Authorization bearer token via `hx-on::before-request` when needed.
- Fragment handlers should return useful UI fragments on both success and validation failure.
- Keep feed selector state synchronized with URL/query state during HTMX navigation.

## Verification

- Run lint and relevant tests after changes.
- Confirm compile/build success before finishing.
