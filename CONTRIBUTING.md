# Contributing

Thanks for helping. The most useful thing right now is **testing on a real Steam Deck**: work through [docs/research/deck-acceptance-checklist.md](docs/research/deck-acceptance-checklist.md) and open an issue with your results, SteamOS version and anything that failed.

## Bugs and ideas

Open an issue. For a bug, include:

- the output of `deckchat doctor` (run it in Konsole, ideally in Game Mode with WoW open)
- `journalctl --user -u deckchat -n 100`
- your SteamOS version and phone browser

Remove anything private from logs first. Chat text isn't logged, but player names in your report might be.

## Code changes

1. Fork the repository and make a branch.
2. Make the change, with tests.
3. Run the checks:
   ```sh
   make test         # Go and phone unit tests
   make lint-addon   # luacheck (needs Docker)
   make test-linux   # Linux integration tests (needs Docker), if you touched companion/internal/input or focus
   ```
4. Open a pull request saying what changed and how you tested it.

## Ground rules

These keep DeckChat private and as far from Blizzard's rules as possible. Changes that break them won't be merged:

- **Local only.** No internet connections, analytics, accounts or update checks, and no third-party requests from the phone page. `npm run check:local` enforces part of this.
- **Only what the player writes.** One message or paste per tap. Never type game keys, never send on a timer, never automate anything in the game.
- **Never touch the game.** No reading or changing game memory, files (apart from the chat log and the add-on folder) or network traffic.

By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE).
