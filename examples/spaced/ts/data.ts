/**
 * data.ts — hardcoded mission/vehicle/competitor/daemon data for spaced.
 * Mirrors Go's data.go exactly for parity testing.
 */

// ---------------------------------------------------------------------------
// Missions
// ---------------------------------------------------------------------------

export interface Mission {
  name: string;
  vehicle: string;
  date: string;
  outcome: string;
  notes: string[];
  market_mood: string[];
}

export const MISSIONS: Mission[] = [
  {
    name: 'Starman',
    vehicle: 'Falcon Heavy',
    date: '2018-02-06',
    outcome: 'SUCCESS',
    notes: [
      "Launched Elon's personal Tesla Roadster into heliocentric orbit.",
      'Mannequin named Starman wearing SpaceX suit still up there.',
      'Played Space Oddity. Once. Then left it on repeat forever.',
    ],
    market_mood: ['📈 briefly', '🚀 vibes only', '🌌 existential'],
  },
  {
    name: 'Crew Dragon Demo-2',
    vehicle: 'Falcon 9',
    date: '2020-05-30',
    outcome: 'SUCCESS',
    notes: [
      'First crewed orbital spaceflight from US soil since 2011.',
      'Brought astronauts Behnken and Hurley to ISS.',
      'SpaceX declared it "basically routine" immediately.',
    ],
    market_mood: ['📈 patriotically', '🇺🇸 aggressively', '🧑‍🚀 vibes'],
  },
  {
    name: 'SN8',
    vehicle: 'Starship',
    date: '2020-12-09',
    outcome: 'RUD',
    notes: [
      'Reached 12.5 km altitude. Belly-flopped successfully.',
      'Header tank pressure low on descent. Exploded on landing.',
      'Elon called it a success. FAA had questions.',
    ],
    market_mood: ['🔥 "nominal"', '💥 technically', '📉 briefly'],
  },
  {
    name: 'SN15',
    vehicle: 'Starship',
    date: '2021-05-05',
    outcome: 'SUCCESS',
    notes: [
      'First Starship prototype to land intact.',
      'Small fire after landing. Extinguished. Declared a win.',
      'SN8–SN14 could not be reached for comment.',
    ],
    market_mood: ['📈 dramatically', '🎉 cautiously', '🔥 residually'],
  },
  {
    name: 'Inspiration4',
    vehicle: 'Falcon 9',
    date: '2021-09-15',
    outcome: 'SUCCESS',
    notes: [
      'First all-civilian orbital mission. Netflix documentary followed.',
      'Billionaire Jared Isaacman funded it. Everyone very inspired.',
      'Crew spent 3 days in orbit. No ISS visit. Just vibes.',
    ],
    market_mood: ['📈 inspirationally', '🎬 cinématically', '💸 expensively'],
  },
  {
    name: 'IFT-1',
    vehicle: 'Starship',
    date: '2023-04-20',
    outcome: 'RUD',
    notes: [
      'Starship reached Max-Q then became unscheduled fireworks.',
      'Launch pad destroyed. FAA launched investigation.',
      'SpaceX called it "a great day for data collection."',
    ],
    market_mood: ['💥 spectacularly', '📋 regulatorily', '🔥 expansively'],
  },
  {
    name: 'IFT-2',
    vehicle: 'Starship',
    date: '2023-11-18',
    outcome: 'RUD',
    notes: [
      'Made it to stage separation this time. Progress.',
      'Ship exploded. Booster exploded. FAA remained unimpressed.',
      'Hot staging ring survived longer than expected.',
    ],
    market_mood: ['📈 marginally', '💥 again', '📋 still regulatorily'],
  },
  {
    name: 'IFT-3',
    vehicle: 'Starship',
    date: '2024-03-14',
    outcome: 'PARTIAL',
    notes: [
      'Made it to space. Both stages lost during reentry.',
      'Ship survived longest yet — 49 minutes of flight.',
      'Plasma blackout during reentry deemed "informative."',
    ],
    market_mood: ['📈 genuinely', '🌡️ thermally', '📡 mostly'],
  },
  {
    name: 'IFT-4',
    vehicle: 'Starship',
    date: '2024-06-06',
    outcome: 'SUCCESS',
    notes: [
      'Both stages survived reentry for the first time.',
      'Ship splashed down softly. Booster nailed soft splashdown too.',
      'No explosions. Twitter in shock.',
    ],
    market_mood: ['📈 surprisingly', '🎉 actually', '💧 splashily'],
  },
  {
    name: 'IFT-5',
    vehicle: 'Starship',
    date: '2024-10-13',
    outcome: 'SUCCESS',
    notes: [
      'Mechazilla caught the booster with chopstick arms mid-air.',
      'Humanity collectively lost its mind.',
      'Ship reentered and splashed down. Mechazilla now has a PR agent.',
    ],
    market_mood: ['🤯 historically', '🦾 mechanically', '📈 violently'],
  },
  {
    name: 'IFT-6',
    vehicle: 'Starship',
    date: '2025-01-16',
    outcome: 'SUCCESS',
    notes: [
      'Second chopstick catch. Mechazilla is now boring (good).',
      'Ship survived reentry and splashed down in Indian Ocean.',
      'SpaceX declared operations "proceeding nominally." We believe them now.',
    ],
    market_mood: ['📈 routinely', '🦾 again', '🌊 oceanically'],
  },
  {
    name: 'DOGE-1',
    vehicle: 'Falcon 9',
    date: '2021-06-30',
    outcome: 'SUCCESS',
    notes: [
      'First commercial mission paid entirely in Dogecoin.',
      'Lunar payload from Geometric Energy Corporation.',
      'Elon tweeted "to the moon" unironically.',
    ],
    market_mood: ['🐕 financially', '📈 meme-ically', '🌕 literally'],
  },
  {
    name: 'CRS-20',
    vehicle: 'Dragon (Cargo)',
    date: '2020-03-06',
    outcome: 'SUCCESS',
    notes: [
      'Last Dragon 1 cargo mission. Carried 1,977 kg of supplies.',
      'Docked autonomously with ISS.',
      'Nobody made a big deal about it. That is the big deal.',
    ],
    market_mood: ['📦 efficiently', '🚀 quietly', '📈 boringly'],
  },
  {
    name: 'Starlink v1.5 L1',
    vehicle: 'Falcon 9',
    date: '2022-02-03',
    outcome: 'PARTIAL',
    notes: [
      '49 satellites deployed. Geomagnetic storm destroyed 38 of them.',
      'Atmospheric drag increased due to solar activity.',
      'Elon blamed the Sun. The Sun did not comment.',
    ],
    market_mood: ['☀️ adversarially', '📡 partially', '📉 atmospherically'],
  },
];

// ---------------------------------------------------------------------------
// Vehicles
// ---------------------------------------------------------------------------

export interface Vehicle {
  name: string;
  type: string;
  status: string;
  reusable: boolean;
  launches: number;
  fun_fact: string;
  systems: Record<string, string>;
}

export const VEHICLES: Vehicle[] = [
  {
    name: 'Falcon 9',
    type: 'Orbital Launch Vehicle',
    status: 'OPERATIONAL',
    reusable: true,
    launches: 300,
    fun_fact: 'Booster B1058 flew 19 times. Retired before it could unionize.',
    systems: {
      propulsion: 'Merlin 1D x9 (first stage), Merlin Vacuum (second stage)',
      guidance: 'Inertial + GPS. Dragon does the real thinking.',
      recovery: 'Grid fins + landing legs. Lands on drone ships named after sci-fi AIs.',
      fairing: 'Two halves. Caught by Mr. Steven (net boat). Sometimes.',
    },
  },
  {
    name: 'Falcon Heavy',
    type: 'Heavy Lift Launch Vehicle',
    status: 'OPERATIONAL',
    reusable: true,
    launches: 12,
    fun_fact: 'Three Falcon 9 cores strapped together. Elon calls this "engineering."',
    systems: {
      propulsion: '27 Merlin 1D engines at liftoff. Neighbors have opinions.',
      core_recovery: 'Center core historically confused about landing.',
      side_boosters: 'Land simultaneously like a budget sci-fi movie.',
      payload: 'Up to 63,800 kg to LEO. Enough for one Tesla and feelings.',
    },
  },
  {
    name: 'Starship',
    type: 'Super Heavy Lift Launch Vehicle',
    status: 'FLIGHT_TEST',
    reusable: true,
    launches: 6,
    fun_fact: 'Most powerful rocket ever built. Also most expensive campfire.',
    systems: {
      propulsion: '33 Raptor engines (Super Heavy) + 6 Raptor Vacuums (Ship)',
      mechazilla: 'Chopstick catch mechanism. Caught booster IFT-5, IFT-6. No prior experience.',
      heat_shield: 'Hexagonal ceramic tiles. 18,000 of them. Each hand-placed.',
      propellant: 'Liquid methane + liquid oxygen. SpaceX grows neither on-site. Yet.',
    },
  },
  {
    name: 'Dragon (Crew)',
    type: 'Crew Spacecraft',
    status: 'OPERATIONAL',
    reusable: true,
    launches: 14,
    fun_fact: 'First commercial crewed spacecraft. Boeing is still working on theirs.',
    systems: {
      life_support: 'Handles O2, CO2, temperature, humidity. Better than some offices.',
      abort: 'SuperDraco engines fire in 0.5 seconds. Hope you never need them.',
      docking: 'NASA docking system. Docks autonomously. Astronauts optional.',
      trunk: 'Unpressurized cargo section. Removed and deorbited each flight.',
    },
  },
  {
    name: 'Dragon (Cargo)',
    type: 'Cargo Spacecraft',
    status: 'OPERATIONAL',
    reusable: true,
    launches: 30,
    fun_fact: 'Has returned more cargo from space than any other vehicle. NASA grateful.',
    systems: {
      berthing: 'Grappled by ISS robotic arm (Canadarm2). Canada involved.',
      cargo: 'Pressurized + unpressurized compartments. Fridge optional.',
      reentry: 'Splashes down in Pacific. Recovery boats on standby.',
      reuse: 'Each Dragon reused up to 5 times. Frugality via ocean.',
    },
  },
  {
    name: 'Mechazilla',
    type: 'Launch/Catch Tower',
    status: 'OPERATIONAL',
    reusable: false,
    launches: 0,
    fun_fact: 'Named after a kaiju. Catches rockets. Still scarier than the kaiju.',
    systems: {
      arms: 'Two articulated "chopstick" arms. 40m long. One job: catch Starship.',
      height: '146 meters. Visible from Boca Chica beach. Also from space.',
      quick_disconnect: 'Umbilical tower with propellant, power, comms. Retracts at T-0.',
      foundation: 'Built on Texas soil. Texas has no opinions about regulations.',
    },
  },
];

// ---------------------------------------------------------------------------
// Competitors
// ---------------------------------------------------------------------------

export interface Competitor {
  name: string;
  founded: number;
  rockets: string[];
  crewed_flights: number;
  launch_success_rate: string;
  notable_achievement: string;
  notable_failure: string;
  elon_opinion: string;
  metrics: Record<string, string>;
}

export const COMPETITORS: Competitor[] = [
  {
    name: 'Boeing',
    founded: 1916,
    rockets: ['Atlas V (via ULA)', 'Vulcan (via ULA)', 'SLS (NASA)'],
    crewed_flights: 1,
    launch_success_rate: '94% (rockets) / 50% (Starliner crewed attempts)',
    notable_achievement: 'Built more aircraft than anyone. Also the Moon rocket.',
    notable_failure: 'Starliner left two astronauts on ISS for 9 months. NASA used SpaceX to retrieve them.',
    elon_opinion: '"Old space." Said with the energy of someone who just won.',
    metrics: {
      cost_per_kg_leo: '$54,000 (SLS) — with apologies',
      sls_launches: '2 (Artemis I, II). $4.1B per launch at last count.',
      starliner_status: 'Grounded. Propulsion leaks. Helium leaks. Hope leaking.',
      market_cap: '~$100B. Was more before the Max 8.',
    },
  },
  {
    name: 'Blue Origin',
    founded: 2000,
    rockets: ['New Shepard', 'New Glenn'],
    crewed_flights: 7,
    launch_success_rate: '96% (New Shepard) / 100% (New Glenn, 3 flights)',
    notable_achievement: 'New Shepard safely flew 31 passengers to suborbital space.',
    notable_failure: 'Founded same year as SpaceX. Currently catching up.',
    elon_opinion: '"Blue who?" — paraphrasing. Not paraphrasing.',
    metrics: {
      new_shepard_altitude: '107 km — over Kármán line, under orbit',
      new_glenn_payload_leo: '45,000 kg — competitive, actually',
      jeff_bezos_involvement: 'Executive Chairman. Flew on New Shepard. Hat was enormous.',
      be4_engine: 'Powers New Glenn and ULA Vulcan. One customer base.',
    },
  },
  {
    name: 'Virgin Galactic',
    founded: 2004,
    rockets: ['VSS Unity', 'VSS Imagine (retired)', 'Delta class (planned)'],
    crewed_flights: 6,
    launch_success_rate: '85% (SpaceShipTwo flights)',
    notable_achievement: 'First commercial space tourism flights. Branson flew himself.',
    notable_failure: 'VSS Enterprise broke apart in 2014. CoF unlocked by pilot error.',
    elon_opinion: 'No public opinion. The silence speaks volumes.',
    metrics: {
      max_altitude: '90 km — below Kármán line per FAI',
      ticket_price: '$450,000 per seat. Includes wings pin.',
      flight_frequency: '3 in 2023, then suspended for Delta class development',
      delta_class_eta: 'Late 2026. Optimistically.',
    },
  },
  {
    name: 'ULA',
    founded: 2006,
    rockets: ['Atlas V', 'Delta IV Heavy', 'Vulcan Centaur'],
    crewed_flights: 0,
    launch_success_rate: '99%',
    notable_achievement: 'Most reliable US launch provider. 100+ consecutive successes.',
    notable_failure: 'Lost NSSL contracts to SpaceX. Atlas V to be retired.',
    elon_opinion: '"Subsidized. Expensive. Good at reliability, bad at price." Fair.',
    metrics: {
      atlas_v_cost: '$150M–$200M per launch',
      vulcan_cost: 'Classified (DoD) / ~$100M commercial',
      be4_dependency: 'Vulcan uses Blue Origin BE-4. Supply chain awkward.',
      launch_record: '155 launches, 155 successes as of 2024.',
    },
  },
  {
    name: 'Roscosmos',
    founded: 1992,
    rockets: ['Soyuz', 'Proton', 'Angara'],
    crewed_flights: 200,
    launch_success_rate: '97% (Soyuz) / 93% (Proton)',
    notable_achievement: 'Soyuz is oldest operational crewed vehicle. Still works.',
    notable_failure: 'Soyuz MS-10 aborted mid-launch 2018. Crew survived. Program did not.',
    elon_opinion: 'No comment after ISS partnership status became "complicated."',
    metrics: {
      soyuz_cost_per_seat: '$90M (pre-2020 NASA contract). Dragon cheaper now.',
      proton_status: 'Being phased out. Angara replacing it. Slowly.',
      lunar_plans: 'Luna-25 crashed into Moon in 2023. Luna-26 TBD.',
      sanctions_impact: 'Western components banned. GPS chips hard to source.',
    },
  },
];

// ---------------------------------------------------------------------------
// Daemons
// ---------------------------------------------------------------------------

export interface DaemonRef {
  outlet: string;
  url: string;
  headline: string;
}

export interface Daemon {
  id: string;
  title: string;
  status: string;
  started: string;
  description: string;
  refs: DaemonRef[];
}

export const DAEMONS: Daemon[] = [
  {
    id: 'funding-secured',
    title: 'Funding Secured Tweet',
    status: 'RUNNING',
    started: '2018-08-07',
    description: 'Elon tweeted "Am considering taking Tesla private at $420. Funding secured." ' +
      'SEC sued. $40M settlement. Tesla remained public. Tweet immortal.',
    refs: [
      {
        outlet: 'SEC',
        url: 'https://www.sec.gov/news/press-release/2018-226',
        headline: 'Elon Musk Settles SEC Fraud Charges; Tesla Charged With and Settles Related Charges',
      },
      {
        outlet: 'NYT',
        url: 'https://www.nytimes.com/2018/08/07/business/tesla-elon-musk-private.html',
        headline: 'Elon Musk Stuns Tesla With Tweet About Going Private',
      },
      {
        outlet: 'Reuters',
        url: 'https://www.reuters.com/article/us-tesla-musk-sec-idUSKCN1MK1SI',
        headline: 'Elon Musk, SEC settle fraud charges over Tesla tweet',
      },
    ],
  },
  {
    id: 'twitter-acquisition-chaos',
    title: 'Twitter/X Acquisition & Aftermath',
    status: 'RUNNING',
    started: '2022-04-14',
    description: 'Bought Twitter for $44B. Fired 75% of staff. Renamed to X. ' +
      'Advertisers left. Revenue halved. Called remaining users "based."',
    refs: [
      {
        outlet: 'WSJ',
        url: 'https://www.wsj.com/articles/elon-musk-completes-44-billion-acquisition-of-twitter-11667090860',
        headline: 'Elon Musk Completes $44 Billion Acquisition of Twitter',
      },
      {
        outlet: 'The Verge',
        url: 'https://www.theverge.com/2022/10/28/23428132/elon-musk-twitter-acquisition-timeline-problems-lawsuit',
        headline: 'Everything that happened between Elon Musk and Twitter',
      },
      {
        outlet: 'Bloomberg',
        url: 'https://www.bloomberg.com/news/articles/2023-10-04/x-revenue-has-fallen-50-since-musk-took-over-twitter',
        headline: 'X Revenue Has Fallen 50% Since Musk Took Over Twitter',
      },
    ],
  },
  {
    id: 'doge-conflict-of-interest',
    title: 'DOGE Conflict of Interest',
    status: 'RUNNING',
    started: '2025-01-20',
    description: 'Appointed head of DOGE advisory body while SpaceX/Tesla receive ' +
      'federal contracts. ProPublica tracked at least $38B in government business.',
    refs: [
      {
        outlet: 'ProPublica',
        url: 'https://www.propublica.org/article/elon-musk-doge-spacex-tesla-government-contracts',
        headline: 'Elon Musk\'s Companies Have Billions in Government Business. He\'s Also Running DOGE.',
      },
      {
        outlet: 'NYT',
        url: 'https://www.nytimes.com/2025/02/01/us/politics/musk-doge-conflicts-interest.html',
        headline: 'Musk\'s Vast Conflicts of Interest Draw Scrutiny as He Wields Influence',
      },
      {
        outlet: 'WaPo',
        url: 'https://www.washingtonpost.com/business/2025/01/28/musk-doge-conflict-interest/',
        headline: 'Musk\'s DOGE role creates sweeping conflicts of interest, experts say',
      },
    ],
  },
  {
    id: 'starship-faa-delays',
    title: 'Starship FAA Regulatory Delays',
    status: 'RESOLVED',
    started: '2021-09-17',
    description: 'FAA environmental review for Boca Chica delayed IFT-1 by 14 months. ' +
      'Elon blamed regulators publicly. FAA issued fines for unlicensed launches.',
    refs: [
      {
        outlet: 'Reuters',
        url: 'https://www.reuters.com/business/aerospace-defense/faa-fines-spacex-630000-unlicensed-launches-2023-09-14/',
        headline: 'FAA fines SpaceX $630,000 for unlicensed launches',
      },
      {
        outlet: 'Ars Technica',
        url: 'https://arstechnica.com/science/2022/06/faa-completes-environmental-review-of-boca-chica-launch-site/',
        headline: 'FAA finally completes Starship environmental review after months of delays',
      },
      {
        outlet: 'The Guardian',
        url: 'https://www.theguardian.com/science/2023/apr/20/starship-launch-spacex-rocket',
        headline: 'SpaceX Starship rocket explodes minutes after launch from Texas',
      },
    ],
  },
  {
    id: 'tesla-autopilot-investigations',
    title: 'Tesla Autopilot/FSD Federal Investigations',
    status: 'RUNNING',
    started: '2016-01-01',
    description: 'NHTSA opened multiple probes into Autopilot crashes. DOJ launched ' +
      'criminal investigation. Tesla recalled 2M+ vehicles via OTA update.',
    refs: [
      {
        outlet: 'NHTSA',
        url: 'https://www.nhtsa.gov/vehicle-safety/automated-vehicles-safety',
        headline: 'NHTSA Opens Formal Investigation Into Tesla Autopilot',
      },
      {
        outlet: 'WaPo',
        url: 'https://www.washingtonpost.com/technology/2023/01/27/tesla-autopilot-nhtsa-recall/',
        headline: 'Tesla recalling 362,000 vehicles over Full Self-Driving software concerns',
      },
      {
        outlet: 'NYT',
        url: 'https://www.nytimes.com/2023/01/26/business/tesla-autopilot-recall.html',
        headline: 'Tesla Recalls 362,000 Cars With Its Full Self-Driving System',
      },
    ],
  },
  {
    id: 'spacex-settlement-nlrb',
    title: 'SpaceX NLRB Settlement',
    status: 'RESOLVED',
    started: '2022-06-16',
    description: 'Eight employees fired after circulating open letter criticizing Musk\'s ' +
      'Twitter behavior. NLRB ruled firing illegal. SpaceX settled. Letter remains.',
    refs: [
      {
        outlet: 'NLRB',
        url: 'https://www.nlrb.gov/case/31-CA-284428',
        headline: 'SpaceX NLRB Case 31-CA-284428',
      },
      {
        outlet: 'Bloomberg',
        url: 'https://www.bloomberg.com/news/articles/2023-07-20/spacex-settles-us-labor-board-case-over-firing-of-employees',
        headline: 'SpaceX Settles US Labor Board Case Over Firing of Employees',
      },
      {
        outlet: 'Reuters',
        url: 'https://www.reuters.com/business/spacex-settles-nlrb-complaint-firing-employees-who-criticized-musk-2023-07-20/',
        headline: 'SpaceX settles NLRB complaint over firing of employees who criticized Musk',
      },
    ],
  },
  {
    id: 'neuralink-animal-welfare',
    title: 'Neuralink Animal Welfare Investigations',
    status: 'RUNNING',
    started: '2022-11-28',
    description: 'Reuters reported roughly 1,500 animals died in Neuralink experiments. ' +
      'USDA investigated. FDA rejected initial human-trial application. Humans enrolled 2024.',
    refs: [
      {
        outlet: 'Reuters',
        url: 'https://www.reuters.com/technology/neuralink-has-faced-federal-probe-animal-welfare-law-violations-2022-12-05/',
        headline: 'Neuralink has faced federal probe for potential animal-welfare violations',
      },
      {
        outlet: 'WaPo',
        url: 'https://www.washingtonpost.com/technology/2022/12/05/neuralink-animal-cruelty-elon-musk/',
        headline: 'Elon Musk\'s Neuralink faces federal inquiry over animal testing',
      },
    ],
  },
  {
    id: 'sec-vs-elon-twitter-poll',
    title: 'SEC vs. Elon: Tesla Stock Twitter Poll',
    status: 'RESOLVED',
    started: '2021-11-06',
    description: 'Elon polled Twitter on whether to sell 10% of Tesla stock. ' +
      'Said he\'d abide by result. SEC asked if that was a securities violation. It was not. ' +
      'He sold $16.4B of stock anyway.',
    refs: [
      {
        outlet: 'SEC',
        url: 'https://www.sec.gov/news/statement/gensler-statement-twitter-poll-111221',
        headline: 'SEC Chair Gensler Statement on Musk Twitter Poll and Securities Laws',
      },
      {
        outlet: 'Bloomberg',
        url: 'https://www.bloomberg.com/news/articles/2021-11-07/musk-says-he-ll-sell-10-of-tesla-stock-if-twitter-votes-yes',
        headline: 'Musk Says He\'ll Sell 10% of Tesla Stock if Twitter Votes Yes',
      },
      {
        outlet: 'NYT',
        url: 'https://www.nytimes.com/2021/11/09/business/elon-musk-tesla-stock-sell.html',
        headline: 'Elon Musk Begins Selling Tesla Shares After Twitter Poll',
      },
    ],
  },
];

// ---------------------------------------------------------------------------
// Elon quotes pool
// ---------------------------------------------------------------------------

export const ELON_QUOTES: string[] = [
  'The first step is to establish that something is possible; then probability will occur.',
  'I think it\'s very important to have a feedback loop.',
  'When something is important enough, you do it even if the odds are not in your favor.',
  'Persistence is very important. You should not give up unless you are forced to give up.',
  'If something is important enough, even if the odds are against you, you should still do it.',
  'It\'s OK to have your eggs in one basket as long as you control what happens to that basket.',
  'The key to making things affordable is to make them in high volume.',
  'I don\'t spend my time pontificating about high-concept things; I spend my time solving engineering and manufacturing problems.',
  'Some people don\'t like change, but you need to embrace change if the alternative is disaster.',
  'My biggest mistake is probably weighing too much on someone\'s talent and not enough on their personality.',
  'People should pursue what they\'re passionate about. That will make them happier than pretty much anything else.',
  'You want to be extra rigorous about making the best possible thing you can. Find everything that\'s wrong with it and fix it.',
  'I think we have a duty to maintain the light of consciousness to make sure it continues into the future.',
  'If you\'re co-founder or CEO, you have to do all kinds of tasks you might not want to do.',
  'I always invest my own money in the companies that I create. I don\'t believe in asking other people to invest in something if I\'m not prepared to do so myself.',
  'The problem is that at a lot of big companies, process becomes a substitute for thinking.',
  'It is possible for ordinary people to choose to be extraordinary.',
  'Really pay attention to negative feedback and solicit it, particularly from friends. Hardly anyone does that, and it\'s incredibly helpful.',
  'Work like hell. I mean you just have to put in 80 to 100 hour weeks every week.',
  'Starting and growing a business is as much about the innovation, drive and determination of the people who do it as it is about the product they sell.',
];

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Pool-based random pick. Seed: Date.now() % pool.length. Deterministic per ms. */
export function pick<T>(pool: T[]): T {
  return pool[Date.now() % pool.length];
}

export function findMission(name: string): Mission | undefined {
  const lower = name.toLowerCase();
  return MISSIONS.find(m => m.name.toLowerCase() === lower);
}

export function findVehicle(name: string): Vehicle | undefined {
  const lower = name.toLowerCase();
  return VEHICLES.find(v => v.name.toLowerCase() === lower);
}

export function findCompetitor(name: string): Competitor | undefined {
  const lower = name.toLowerCase();
  return COMPETITORS.find(c => c.name.toLowerCase() === lower);
}

export function findDaemon(id: string): Daemon | undefined {
  const lower = id.toLowerCase();
  return DAEMONS.find(d => d.id.toLowerCase() === lower);
}
