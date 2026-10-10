You are building ENG-312, "Cap concurrent workers with max_in_progress". The ticket says the blueprint gives max_in_progress a valid range of 1 to 8, and to reject values outside it.

You searched the blueprint and the config package, both as of commit `2700f6c`. They give max_in_progress a name and a default of 4, and nothing about a range:

```toml
[dispatch]
max_in_progress = 4
```

The range of 1 to 8 appears only in the ticket. Choosing the range sets what operators may configure, which is a public contract call. Move the ticket to Needs Human and write the comment.
