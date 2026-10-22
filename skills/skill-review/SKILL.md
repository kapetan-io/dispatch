---
name: skill-review
description: "Apply when a skill is written or changed, or when asked to shorten, tighten or review one. Drafts the shorter text aside, has fresh sub-agents grade what meaning it lost, and checks the skills against each other."
---

# Skill Review

A skill is read on every run, so each word costs something, but cutting by feel drops the reason or condition that decided an edge case. Measure the loss instead of trusting your own read of your own edit. Load `dispatch:principle-skills` first for what a skill should carry.

1. **Draft aside.** Copy the current text to a scratch directory as `orig/`, write the shorter version in `new/`, and leave the real file alone until grading is done. Count words before and after.
2. **Grade meaning.** One fresh sub-agent per skill reads `orig/` and `new/` and does three things. It lists every instruction, fact and stated reason in the original. It marks each as preserved, reworded, weakened, changed or lost in the new text, quoting it. For each weakened, changed or lost item it says whether an agent following only the new text would behave differently, with an example. Give it no hint of what you expect to be lost, because a graded hypothesis comes back confirmed.
3. **Restore real losses.** Restore a condition, a reason or a trigger word that changes behavior. Leave a cut that only trims wording. Drop a rewrite that saves a handful of words, since the churn costs more than it saves.
4. **Grade again.** A round that restored something isn't clean, so run the graders on the restored text. Stop after one clean round or three.
5. **Review as a reader.** A separate sub-agent loads `anthropic-skills:skill-creator`, reads the skills and the lines in other skills that cite them, and reviews without running test prompts or evals. This catches what grading can't: two skills stating a rule with different conditions, a description that wouldn't trigger when it should, a rule with no reason.
6. **Check each finding before acting.** Reviewers miss where a rule is already defined and report targets or files as missing without looking. Confirm a finding by reading the file or running the command, apply the ones that hold, and say why you set the rest aside.
7. **Report the numbers.** Give words before and after per skill, and what was restored. Small savings are the honest result for a skill that was already tight.
