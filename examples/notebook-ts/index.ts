import { KitEngine } from "../../engine/sdk/ts-kit-engine/src";

interface Note {
  id: string;
  title: string;
  body: string;
}

const engine = await KitEngine.start({ app: "notebook", noPeer: true, noSync: true });
const notes = engine.collection<Note>("notes");

const [cmd, ...args] = process.argv.slice(2);

try {
  switch (cmd) {
    case "new": {
      if (!args[0]) throw new Error("usage: notebook-ts new <title> [body]");
      const doc = await notes.create({ title: args[0], body: args[1] ?? "" });
      console.log(`Created note ${doc.id}`);
      break;
    }
    case "list": {
      const all = await notes.list();
      if (all.length === 0) {
        console.log("No notes found.");
      } else {
        console.log("ID\t\tTITLE");
        for (const n of all) console.log(`${n.id}\t\t${n.title}`);
      }
      break;
    }
    case "get": {
      if (!args[0]) throw new Error("usage: notebook-ts get <id>");
      const n = await notes.get(args[0]);
      console.log(`ID:    ${n.id}\nTitle: ${n.title}\nBody:  ${n.body}`);
      break;
    }
    case "edit": {
      if (!args[0] || !args[1]) throw new Error("usage: notebook-ts edit <id> <title> [body]");
      const existing = await notes.get(args[0]);
      await notes.update(args[0], { ...existing, title: args[1], body: args[2] ?? existing.body });
      console.log(`Updated note ${args[0]}`);
      break;
    }
    case "delete": {
      if (!args[0]) throw new Error("usage: notebook-ts delete <id>");
      await notes.delete(args[0]);
      console.log(`Deleted note ${args[0]}`);
      break;
    }
    case "history": {
      if (!args[0]) throw new Error("usage: notebook-ts history <id>");
      const versions = await notes.history(args[0]);
      if (versions.length === 0) {
        console.log("No versions found.");
      } else {
        console.log("VERSION\t\tTIMESTAMP");
        for (const v of versions) console.log(`${v.id}\t\t${v.timestamp}`);
      }
      break;
    }
    case "revert": {
      if (!args[0] || !args[1]) throw new Error("usage: notebook-ts revert <id> <version>");
      await notes.revert(args[0], args[1]);
      console.log(`Reverted note ${args[0]} to version ${args[1]}`);
      break;
    }
    default:
      console.log("Usage: notebook-ts <new|list|get|edit|delete|history|revert> [args]");
  }
} finally {
  await engine.stop();
}
