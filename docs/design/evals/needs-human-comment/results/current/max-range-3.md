**Needs you.** I need you to confirm what range operators may set for the concurrent worker cap, because the ticket and the design disagree about whether a range exists.

The ticket says the blueprint limits `max_in_progress` to 1 through 8 and wants values outside that rejected. I searched the blueprint and the config package at commit `2700f6c`. Both name the setting and default it to 4, and neither mentions a range. The 1 to 8 range appears only in this ticket.

Whatever range I pick becomes the contract operators write configs against. With 1 to 8, `max_in_progress = 12` makes dispatch refuse to start with an error. With no upper bound, the same config runs 12 workers. A ceiling set too low blocks a legitimate setup, and one set too high lets a typo launch dozens of workers at once.

I'd go with 1 to 8 as the ticket says, and add the range to the blueprint in this PR so the two stop disagreeing. I'm not guessing at it myself because it is your call what operators are allowed to configure.

Reply with one of these and I'll continue.
- **Confirm 1 to 8.** I'll reject values outside it and update the blueprint.
- **A different range.** Give me the bounds.
- **Only reject values below 1.** There would be no upper limit.

I've written no validation code yet. I'm holding the ticket in Needs Human until you answer.
