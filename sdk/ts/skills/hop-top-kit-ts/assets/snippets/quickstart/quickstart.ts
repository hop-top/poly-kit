import { createCLI } from '@hop-top/kit/cli';
import { dispatch } from '@hop-top/kit/output';

const { program } = createCLI({
  name: 'mytool',
  version: '1.0',
  description: 'does things',
});

const rows = [
  { id: '1', name: 'Alice' },
  { id: '2', name: 'Bob' },
];

program.command('list').action(async () => {
  await dispatch(program, rows, {
    columns: [
      { header: 'id', key: 'id' },
      { header: 'name', key: 'name' },
    ],
  });
});

// A real tool ends with `program.parse()`; the snippet passes its argv.
await program.parseAsync(['list', '--format', 'json'], { from: 'user' });
await program.parseAsync(['list'], { from: 'user' });
