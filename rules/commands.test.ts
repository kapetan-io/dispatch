import { scenario, ticket, comment, rulesFrom } from "dispatch/testing";

const rules = rulesFrom("./commands");

scenario(rules, "old commands never run on first start", s => {
  s.given(ticket("ENG-1", { status: "in_progress" }),
          comment("ENG-1", { body: "/dispatch pause", at: "yesterday" }));
  s.start();
  s.expect([]);
  s.see(comment("ENG-1", { body: "/dispatch pause" }));
  s.expect([["comment-command", "linear.move", "ENG-1", "todo"]]);
});
