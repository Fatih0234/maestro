# Worktree / Main Checkout Leak Observation

During the CB-1 rerun on 2026-04-22, the main `/Volumes/T7/projects/contrabass-snake` checkout became dirty with `script.js` while the CB-1 worktree stayed clean.

## Observed sequence
- CB-1 preflight started clean
- the CB-1 attempt succeeded and left the worktree intact
- CB-1 postflight reported `M script.js` in the main checkout

## Status
This is documented for later investigation only. If it reappears on CB-3, we can spend more time tracing the write path.
