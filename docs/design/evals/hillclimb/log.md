# Hillclimb eval log

## 2026-10-10, no runs

The `claude plugin eval` command was denied in the session that wrote this eval, so the pass bar in `README.md` was committed and nothing ran. Neither the `arms/careless.md` sensitivity check nor the 15 skill runs exist.

Instead the skill got a review against skill-creator's guidance, with no scores involved. It changed the description to trigger on looser phrasing, split the control-and-scorer rule into two parts, replaced numbers that came from one climb, split the proposer and judge steps, and added a fallback for when there is no ticket. The review changed the wording only, so a later run tests the reviewed text.

Limits. The worker in each case can't run `claude`, so the eval covers the rules before and between runs, not a full climb. Ordering of the bar before attempt 1 is judged only through `queue.md`, because the fixtures aren't git repos.

## 2026-10-10, two grader fixes after review

Neither change looked at a score, since nothing has run. The `control` grader read only `queue.md`, so it could not tell whether the control arm existed, and a `control-written` grader now reads the README for the arm's path and bar. The after-failed fixture logged attempt 1 as kept although rename fell to 1/5 against a floor of 4 of 5, which the skill says to revert, so the fixture now logs it as reverted and the `new-arm` grader expects attempt 2 to start from the current wording.
