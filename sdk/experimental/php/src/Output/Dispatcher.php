<?php

declare(strict_types=1);

namespace HopTop\Kit\Output;

use HopTop\Kit\Output\Formatter\ColumnSpec;
use HopTop\Kit\Output\Formatter\Options as OptionsParser;
use InvalidArgumentException;
use RuntimeException;
use Symfony\Component\Console\Application;
use Symfony\Component\Console\Helper\Helper;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;
use Symfony\Component\Console\Output\StreamOutput;

/**
 * Resolves the active output flags from an InputInterface and renders data,
 * honoring --format, --format-opt, --cols/--columns, --template,
 * --format-help, and --output|-o per the rules wired by Flags::register.
 *
 * Mirrors py output.dispatch, ts dispatch, go output.Dispatch.
 *
 * Resolution order:
 *   1. Resolve writer: empty/'-' → $output stream, else file.
 *   2. --format-help short-circuit: list registry or show one formatter.
 *   3. Resolve format: explicit --format wins; else infer from --output ext.
 *   4. Mismatch detection: explicit --format + ext mapping to different
 *      formatter is a hard error.
 *   5. --template: render template against data, return.
 *   6. Else: parseOptions, validateCols, formatter.render.
 */
final class Dispatcher
{
    private const STDOUT_SENTINEL = '-';
    private const DEFAULT_FORMAT = 'table';

    /**
     * @param list<ColumnSpec>|null $columns Schema for --cols validation
     */
    public static function dispatch(
        InputInterface $input,
        OutputInterface $output,
        mixed $data,
        ?array $columns = null,
        ?Registry $registry = null,
        ?Application $application = null,
    ): void {
        $registry ??= Flags::registryFor($application);

        // 1. Resolve writer.
        $outputPath = self::stringOpt($input, 'output');
        [$writer, $close] = self::resolveWriter($output, $outputPath);

        try {
            // 2. --format-help short-circuit.
            if (self::isFormatHelpSet($input)) {
                $fhVal = $input->getOption('format-help');
                $explicitKey = is_string($fhVal) ? $fhVal : '';
                $key = $explicitKey !== ''
                    ? $explicitKey
                    : (self::isFormatExplicit($input) ? self::stringOpt($input, 'format') : '');
                self::renderFormatHelp($writer, $registry, $key);
                return;
            }

            // 3 + 4. Format resolution.
            $format = self::resolveFormat($input, $outputPath, $registry);

            // 5. --template escape hatch.
            $template = self::stringOpt($input, 'template');
            $cols = self::resolveCols($input);
            if ($template !== '') {
                if ($cols !== []) {
                    throw new InvalidArgumentException('--template and --cols are mutually exclusive');
                }
                self::renderTemplate($writer, $template, $data, $columns);
                return;
            }

            // 6. Formatter render.
            $formatter = $registry->lookup($format);
            if ($formatter === null) {
                throw new InvalidArgumentException(sprintf(
                    "unknown output format '%s' (valid: %s)",
                    $format,
                    implode(', ', $registry->keys()),
                ));
            }
            /** @var list<string> $pairs */
            $pairs = (array) $input->getOption('format-opt');
            $opts = OptionsParser::parse(array_values($pairs), $formatter->options());

            if ($cols !== [] && $columns !== null && $columns !== []) {
                self::validateCols($cols, $columns);
            }

            $formatter->render($writer, $data, $opts, $cols);
        } finally {
            $close();
        }
    }

    /**
     * Resolve a writer for the dispatch. When --output is empty or '-' we
     * write through an in-memory buffer and flush to OutputInterface on
     * close (so BufferedOutput, ConsoleOutput, and TestOutput all see the
     * bytes). When --output points to a file, we write directly to it.
     *
     * @return array{0:resource,1:callable():void}
     */
    private static function resolveWriter(OutputInterface $output, string $path): array
    {
        if ($path === '' || $path === self::STDOUT_SENTINEL) {
            // StreamOutput exposes the raw stream — write directly so the
            // user's symfony output decorators (e.g. ANSI stripping) apply.
            if ($output instanceof StreamOutput) {
                return [$output->getStream(), static function (): void {}];
            }
            // For non-stream OutputInterface (BufferedOutput, TestOutput,
            // NullOutput...), buffer through memory + flush via write().
            $buf = fopen('php://memory', 'w+b');
            if (!is_resource($buf)) {
                throw new RuntimeException('output: cannot open memory buffer');
            }
            return [$buf, static function () use ($buf, $output): void {
                rewind($buf);
                $bytes = stream_get_contents($buf) ?: '';
                fclose($buf);
                // OutputInterface::write expects no trailing newline duplication;
                // formatters already emit "\n", so pass newline=false.
                $output->write($bytes, newline: false);
            }];
        }
        if (is_dir($path)) {
            throw new InvalidArgumentException(sprintf("output path '%s' is a directory", $path));
        }
        $fh = @fopen($path, 'wb');
        if ($fh === false) {
            throw new RuntimeException(sprintf("output: cannot open '%s' for writing", $path));
        }
        return [$fh, static function () use ($fh): void {
            if (is_resource($fh)) {
                fclose($fh);
            }
        }];
    }

    private static function isFormatHelpSet(InputInterface $input): bool
    {
        if (!$input->hasOption('format-help')) {
            return false;
        }
        // Distinguishing "bare --format-help" (value === null) from "not
        // passed at all" (default === false): the default is `false`, so
        // anything that isn't false means the user passed the flag.
        // ArrayInput sets null when bound with no value supplied.
        return $input->hasParameterOption('--format-help', true)
            || $input->getOption('format-help') !== false;
    }

    private static function isFormatExplicit(InputInterface $input): bool
    {
        // Symfony Console can't easily distinguish "user passed --format=table"
        // from "default applied". Approximate via raw token scan on the
        // original argv stash (good enough for parity; tests cover both).
        $tokens = method_exists($input, '__toString') ? (string) $input : '';
        return str_contains($tokens, '--format=') || str_contains($tokens, '--format ');
    }

    private static function resolveFormat(
        InputInterface $input,
        string $outputPath,
        Registry $registry,
    ): string {
        $explicit = self::isFormatExplicit($input);
        $format = self::stringOpt($input, 'format');
        $extKey = '';
        if ($outputPath !== '' && $outputPath !== self::STDOUT_SENTINEL) {
            $ext = strtolower(ltrim((string) pathinfo($outputPath, PATHINFO_EXTENSION), '.'));
            $map = $registry->extensionMap();
            $extKey = $map[$ext] ?? '';
        }
        if ($explicit) {
            if ($extKey !== '' && $extKey !== $format) {
                throw new InvalidArgumentException(sprintf(
                    "--format=%s conflicts with --output extension (.%s → %s)",
                    $format,
                    pathinfo($outputPath, PATHINFO_EXTENSION),
                    $extKey,
                ));
            }
            return $format;
        }
        if ($extKey !== '') {
            return $extKey;
        }
        return $format !== '' ? $format : self::DEFAULT_FORMAT;
    }

    /**
     * @return list<string>
     */
    private static function resolveCols(InputInterface $input): array
    {
        $out = [];
        foreach (['cols', 'columns'] as $opt) {
            if (!$input->hasOption($opt)) {
                continue;
            }
            /** @var list<string> $values */
            $values = (array) $input->getOption($opt);
            foreach ($values as $v) {
                foreach (explode(',', $v) as $piece) {
                    $piece = trim($piece);
                    if ($piece !== '') {
                        $out[] = $piece;
                    }
                }
            }
        }
        return $out;
    }

    /**
     * @param list<string>     $cols
     * @param list<ColumnSpec> $schema
     */
    private static function validateCols(array $cols, array $schema): void
    {
        $headers = array_map(static fn (ColumnSpec $c) => $c->header, $schema);
        foreach ($cols as $c) {
            if (!in_array($c, $headers, true)) {
                throw new InvalidArgumentException(sprintf(
                    "unknown column '%s' (valid: %s)",
                    $c,
                    implode(', ', $headers),
                ));
            }
        }
    }

    /**
     * Minimal `{key}` substitution against each row of $data. Full template
     * engine (eta/Jinja parity) deferred to Phase-3; this is enough for
     * `--template '{name}\t{count}'`-style use cases.
     *
     * @param list<ColumnSpec>|null $columns
     */
    private static function renderTemplate(
        mixed $writer,
        string $template,
        mixed $data,
        ?array $columns,
    ): void {
        unset($columns); // schema not consulted in minimal renderer
        $rows = is_array($data) && array_is_list($data) ? $data : [$data];
        foreach ($rows as $row) {
            $line = preg_replace_callback(
                '/\{([a-zA-Z0-9_.-]+)\}/',
                static function (array $m) use ($row): string {
                    $k = $m[1];
                    if (is_array($row) && array_key_exists($k, $row)) {
                        return (string) $row[$k];
                    }
                    return '';
                },
                $template,
            ) ?? $template;
            if (fwrite($writer, $line . "\n") === false) {
                throw new RuntimeException('template: write failed');
            }
        }
    }

    private static function renderFormatHelp(mixed $writer, Registry $registry, string $key): void
    {
        if ($key === '') {
            $lines = ["Available output formats:"];
            foreach ($registry->formatters() as $f) {
                $lines[] = sprintf("  %-8s  (%s)", $f->key(), implode(', ', $f->extensions()));
            }
            fwrite($writer, implode("\n", $lines) . "\n");
            return;
        }
        $f = $registry->lookup($key);
        if ($f === null) {
            throw new InvalidArgumentException(sprintf(
                "unknown format '%s' (valid: %s)",
                $key,
                implode(', ', $registry->keys()),
            ));
        }
        $out = [sprintf("Format: %s (%s)", $f->key(), implode(', ', $f->extensions()))];
        foreach ($f->options() as $o) {
            $def = $o->default !== null ? sprintf(' [default: %s]', var_export($o->default, true)) : '';
            $out[] = sprintf("  --format-opt %s=<%s>%s  %s", $o->name, $o->type->value, $def, $o->usage);
        }
        fwrite($writer, implode("\n", $out) . "\n");
    }

    private static function stringOpt(InputInterface $input, string $name): string
    {
        if (!$input->hasOption($name)) {
            return '';
        }
        $v = $input->getOption($name);
        return is_string($v) ? $v : '';
    }
}
