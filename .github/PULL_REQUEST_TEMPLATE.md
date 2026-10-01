<!-- Thanks for contributing to vellum! Keep PRs focused — one concern each. -->

## What & why

<!-- What does this change, and what problem does it solve? -->

## Type of change

- [ ] Bug fix
- [ ] New feature
- [ ] UI (follows `design/*.dc.html` / `DESIGN.md`)
- [ ] Docs / chore

## Checklist

- [ ] `go test ./...` passes and `go vet ./...` is clean
- [ ] `gofmt` applied
- [ ] Web UI (if touched): `npm run build` and `npm run lint` pass
- [ ] `CHANGELOG.md` updated (Unreleased) for user-visible changes
- [ ] No secrets in code, tests, or logs
- [ ] Touches a security-sensitive area (path handling in `internal/vault`,
      `internal/auth`, share links, a new outbound connection; see
      `CONTRIBUTING.md`). If so, a test tries the attack, and the notes below
      say which.

## Notes for reviewers

<!-- Screenshots for UI changes, migration notes, anything to watch. -->
