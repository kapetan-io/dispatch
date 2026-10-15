import { on } from "dispatch";

// An example, not in dispatch.ts: today's dispatch acts on no comment text.
// Runs once for each comment posted after dispatch starts. The first read after a
// start is no event, so starting dispatch never re-runs old commands. One posted while
// the laptop slept still runs when it wakes. Dispatch's own comments never count.
on({
  name:  "comment-command",
  event: "linear.comment.posted",
  if:    (c) => !c.byDispatch && c.body.startsWith("/dispatch "),
  then:  (c, ctx) => {
    const [command, ...rest] = c.body.slice("/dispatch ".length).trim().split(" ");
    switch (command) {
      case "tell":
        ctx.herdr.queueNudge(c.ticket, rest.join(" "), { once: ["tell", c.id] });
        break;
      case "pause":
        ctx.linear.queueMove(c.ticket, "todo", `paused by ${c.author}`, { once: ["pause", c.id] });
        break;
      default:
        ctx.linear.queueComment(c.ticket, `Unknown command "${command}". Try "tell" or "pause".`, { once: ["unknown-command", c.id] });
    }
  },
});
