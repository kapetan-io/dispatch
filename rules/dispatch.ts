// Every rule dispatch runs. Each file groups the rules for one part of the job.
// `dispatch rules init` writes these files, so nothing runs that isn't here.
// extras.ts and commands.ts are examples beyond today's behavior, so they aren't imported.
import "./workers";
import "./trees";
import "./parents";
import "./escalations";
import "./nudges";
import "./notices";
import "./merge";
