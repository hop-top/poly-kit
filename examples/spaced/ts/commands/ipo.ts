/**
 * commands/ipo.ts — ipo status
 */

import { Command } from 'commander';

const IPO_TIMELINE = [
  { year: 2012, event: 'Elon says SpaceX IPO possible "someday."' },
  { year: 2015, event: 'SpaceX declines to comment on IPO. Very coy.' },
  { year: 2017, event: 'Elon: "Not planning an IPO." Adds: "Maybe Starlink separately."' },
  { year: 2020, event: 'Starlink IPO hinted. Starlink profitable first. Starlink: not yet.' },
  { year: 2021, event: 'SpaceX valued at $74B in secondary market. IPO: still "someday."' },
  { year: 2022, event: 'Elon too busy buying Twitter to discuss IPO. Market: nervous.' },
  { year: 2023, event: 'SpaceX valued at $150B. IPO: "After Mars mission." (est. 2029.)' },
  { year: 2024, event: 'SpaceX valued at $210B. IPO: "When ready." Ready: undefined.' },
  { year: 2025, event: 'IPO: "Starlink first, maybe." Starlink revenue: $6.6B/yr. Maybe closer.' },
];

export function ipoCommand(): Command {
  const ipo = new Command('ipo')
    .description('Spacex IPO status tracker');

  ipo
    .command('status')
    .description('Current SpaceX IPO status')
    .action(function () {
      console.log('');
      console.log('  SPACEX IPO STATUS');
      console.log(`  ${'─'.repeat(50)}`);
      console.log('');
      console.log('  Status:           PENDING');
      console.log('  Expected:         After Mars (est. never / 2029 / someday)');
      console.log('  Starlink IPO:     POSSIBLE (mood-dependent)');
      console.log('  Current valuation: ~$350B (private market, April 2026)');
      console.log('');
      console.log('  IPO TIMELINE (historic "someday" progression):');
      console.log('');

      for (const entry of IPO_TIMELINE) {
        console.log(`    ${entry.year}  ${entry.event}`);
      }

      console.log('');
      console.log('  Analyst consensus: "We\'ll believe it when we see the S-1."');
      console.log('  SEC stance:        Ready when you are.');
      console.log('  Elon stance:       [tweet pending]');
      console.log('');
      console.log('  Note: Starlink generates ~$6.6B/yr in revenue. The IPO math');
      console.log('  makes sense. The timing does not. Classic Musk.');
      console.log('');
    });

  return ipo;
}
