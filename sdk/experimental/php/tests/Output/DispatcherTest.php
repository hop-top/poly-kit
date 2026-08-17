<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Output;

use HopTop\Kit\Output\Dispatcher;
use HopTop\Kit\Output\Formatter\ColumnSpec;
use HopTop\Kit\Output\Formatter\Builtin\JsonFormatter;
use HopTop\Kit\Output\Formatter\Builtin\TableFormatter;
use HopTop\Kit\Output\Formatter\Builtin\YamlFormatter;
use HopTop\Kit\Output\Registry;
use InvalidArgumentException;
use PHPUnit\Framework\TestCase;
use Symfony\Component\Console\Input\ArrayInput;
use Symfony\Component\Console\Input\InputDefinition;
use Symfony\Component\Console\Input\InputOption;
use Symfony\Component\Console\Output\BufferedOutput;

class DispatcherTest extends TestCase
{
    /** Build an InputInterface with the kit output-flag definition. */
    private function input(array $params): ArrayInput
    {
        $def = new InputDefinition([
            new InputOption('format', null, InputOption::VALUE_REQUIRED, '', 'table'),
            new InputOption('format-opt', null, InputOption::VALUE_REQUIRED | InputOption::VALUE_IS_ARRAY, ''),
            new InputOption('format-help', null, InputOption::VALUE_OPTIONAL, '', false),
            new InputOption('cols', null, InputOption::VALUE_REQUIRED | InputOption::VALUE_IS_ARRAY, ''),
            new InputOption('columns', null, InputOption::VALUE_REQUIRED | InputOption::VALUE_IS_ARRAY, ''),
            new InputOption('template', null, InputOption::VALUE_REQUIRED, ''),
            new InputOption('output', 'o', InputOption::VALUE_REQUIRED, '', ''),
        ]);
        $input = new ArrayInput($params, $def);
        $input->bind($def);
        return $input;
    }

    private function registry(): Registry
    {
        $r = new Registry();
        $r->register(new JsonFormatter());
        $r->register(new YamlFormatter());
        return $r;
    }

    public function testDispatchExplicitJsonToStdout(): void
    {
        $input = $this->input(['--format' => 'json']);
        $output = new BufferedOutput();
        Dispatcher::dispatch($input, $output, [['a' => 1]], registry: $this->registry());
        $this->assertJsonStringEqualsJsonString('[{"a":1}]', $output->fetch());
    }

    public function testDispatchInfersFormatFromOutputExt(): void
    {
        $tmp = tempnam(sys_get_temp_dir(), 'kit_disp_');
        rename($tmp, $tmp . '.yaml');
        $tmp .= '.yaml';
        try {
            $input = $this->input(['--output' => $tmp]);
            Dispatcher::dispatch($input, new BufferedOutput(), ['name' => 'alpha'], registry: $this->registry());
            $content = (string) file_get_contents($tmp);
            $this->assertStringContainsString('name: alpha', $content);
        } finally {
            @unlink($tmp);
        }
    }

    public function testDispatchExplicitFormatConflictsWithExt(): void
    {
        $tmp = tempnam(sys_get_temp_dir(), 'kit_disp_') . '.yaml';
        try {
            $input = $this->input(['--format' => 'json', '--output' => $tmp]);
            $this->expectException(InvalidArgumentException::class);
            $this->expectExceptionMessage('conflicts with --output extension');
            Dispatcher::dispatch($input, new BufferedOutput(), [], registry: $this->registry());
        } finally {
            @unlink($tmp);
        }
    }

    public function testDispatchFormatHelpListsAll(): void
    {
        $input = $this->input(['--format-help' => null]);
        $output = new BufferedOutput();
        Dispatcher::dispatch($input, $output, [], registry: $this->registry());
        $out = $output->fetch();
        $this->assertStringContainsString('json', $out);
        $this->assertStringContainsString('yaml', $out);
    }

    public function testDispatchUnknownFormatRejected(): void
    {
        $input = $this->input(['--format' => 'bogus']);
        $this->expectException(InvalidArgumentException::class);
        $this->expectExceptionMessage("unknown output format 'bogus'");
        Dispatcher::dispatch($input, new BufferedOutput(), [], registry: $this->registry());
    }

    public function testDispatchTemplateMutuallyExclusiveWithCols(): void
    {
        $input = $this->input(['--format' => 'json', '--template' => '{a}', '--cols' => ['a']]);
        $this->expectException(InvalidArgumentException::class);
        $this->expectExceptionMessage('mutually exclusive');
        Dispatcher::dispatch($input, new BufferedOutput(), [['a' => 1]], registry: $this->registry());
    }

    public function testDispatchTemplateRenders(): void
    {
        $input = $this->input(['--format' => 'json', '--template' => '{name}:{count}']);
        $output = new BufferedOutput();
        Dispatcher::dispatch($input, $output, [['name' => 'alpha', 'count' => 1]], registry: $this->registry());
        $this->assertSame("alpha:1\n", $output->fetch());
    }

    public function testDispatchColsValidatedAgainstSchema(): void
    {
        $schema = [ColumnSpec::of('name', 'name'), ColumnSpec::of('count', 'count')];
        $input = $this->input(['--format' => 'json', '--cols' => ['bogus']]);
        $this->expectException(InvalidArgumentException::class);
        $this->expectExceptionMessage("unknown column 'bogus'");
        Dispatcher::dispatch($input, new BufferedOutput(), [['name' => 'x']], columns: $schema, registry: $this->registry());
    }

    public function testDispatchFormatOptForwardsToFormatter(): void
    {
        $input = $this->input(['--format' => 'json', '--format-opt' => ['indent=0']]);
        $output = new BufferedOutput();
        Dispatcher::dispatch($input, $output, ['a' => 1], registry: $this->registry());
        $this->assertSame("{\"a\":1}\n", $output->fetch());
    }

    /**
     * Rule 1: the ColumnSpec schema reaches the formatter and drives the
     * default column order — it is not consumed by validateCols and
     * dropped. Payload key order disagrees with the spec order so only
     * a threaded schema can produce the asserted key sequence.
     */
    public function testDispatchThreadsColumnSpecToFormatter(): void
    {
        $schema = [
            ColumnSpec::of('status', 'status'),
            ColumnSpec::of('name', 'name'),
        ];
        $input = $this->input(['--format' => 'json', '--format-opt' => ['indent=0']]);
        $output = new BufferedOutput();
        Dispatcher::dispatch(
            $input,
            $output,
            [['name' => 'alpha', 'status' => 'ok']],
            columns: $schema,
            registry: $this->registry(),
        );
        $this->assertSame("[{\"status\":\"ok\",\"name\":\"alpha\"}]\n", $output->fetch());
    }

    /**
     * Rule 2: --cols reorders on top of a threaded schema — user wins.
     */
    public function testDispatchColsOverridesColumnSpecOrder(): void
    {
        $schema = [
            ColumnSpec::of('name', 'name'),
            ColumnSpec::of('count', 'count'),
            ColumnSpec::of('status', 'status'),
        ];
        $input = $this->input([
            '--format' => 'json',
            '--format-opt' => ['indent=0'],
            '--cols' => ['status', 'name'],
        ]);
        $output = new BufferedOutput();
        Dispatcher::dispatch(
            $input,
            $output,
            [['name' => 'alpha', 'count' => 1, 'status' => 'ok']],
            columns: $schema,
            registry: $this->registry(),
        );
        $this->assertSame("[{\"status\":\"ok\",\"name\":\"alpha\"}]\n", $output->fetch());
    }

    /**
     * Rule 1, template path: --template honors ColumnSpec order too. The
     * minimal renderer previously did `unset($columns)` and ignored the
     * schema entirely; `{*}` expands to the schema-ordered values.
     */
    public function testDispatchTemplateHonorsColumnSpecOrder(): void
    {
        $schema = [
            ColumnSpec::of('status', 'status'),
            ColumnSpec::of('name', 'name'),
        ];
        $input = $this->input(['--format' => 'json', '--template' => '{*}']);
        $output = new BufferedOutput();
        Dispatcher::dispatch(
            $input,
            $output,
            [['name' => 'alpha', 'count' => 1, 'status' => 'ok']],
            columns: $schema,
            registry: $this->registry(),
        );
        $this->assertSame("ok\talpha\n", $output->fetch());
    }

    /**
     * Rule 1 fallback on the template path: no ColumnSpec → payload key
     * order drives `{*}`.
     */
    public function testDispatchTemplateStarFallsBackToPayloadKeyOrder(): void
    {
        $input = $this->input(['--format' => 'json', '--template' => '{*}']);
        $output = new BufferedOutput();
        Dispatcher::dispatch(
            $input,
            $output,
            [['name' => 'alpha', 'count' => 1]],
            registry: $this->registry(),
        );
        $this->assertSame("alpha\t1\n", $output->fetch());
    }

    /**
     * Rule 3: header == key, so --cols validation matches on the same name
     * used for the row lookup. A ColumnSpec whose key differs from its
     * header is rejected at construction rather than silently splitting.
     */
    public function testColumnSpecRejectsHeaderKeySplit(): void
    {
        $this->expectException(InvalidArgumentException::class);
        $this->expectExceptionMessage("ColumnSpec header 'Name' must equal key 'name'");
        ColumnSpec::of('Name', 'name');
    }

    /**
     * Rule 4: zero rows emits nothing on the table path even when a
     * ColumnSpec supplies headers.
     */
    public function testDispatchZeroRowsWithColumnSpecEmitsNothing(): void
    {
        $registry = $this->registry();
        $registry->register(new TableFormatter());
        $schema = [ColumnSpec::of('name', 'name'), ColumnSpec::of('count', 'count')];
        $input = $this->input(['--format' => 'table']);
        $output = new BufferedOutput();
        Dispatcher::dispatch($input, $output, [], columns: $schema, registry: $registry);
        $this->assertSame('', $output->fetch());
    }
}
