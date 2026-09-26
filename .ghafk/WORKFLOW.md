---
label: agent
checks: go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -count=1 ./...
---
Rules for this repository:
- Use the standard library only.
- Do not write code comments.
- Add a new file only when the issue needs one.
- Change `README.md` only when the issue asks for it.
- Write a test only if you can name a one-line production change that
  breaks it.
