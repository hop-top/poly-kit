"""
data.py — hardcoded mission/vehicle/competitor/daemon data for spaced CLI.

All pools use index: int(time.time()) % len(pool) for deterministic-ish rotation.
"""
from __future__ import annotations

import time
from dataclasses import dataclass, field
from typing import Optional


# ---------------------------------------------------------------------------
# Pool helper
# ---------------------------------------------------------------------------

def _pick(pool: list[str]) -> str:
    idx = int(time.time()) % len(pool)
    return pool[idx]


# ---------------------------------------------------------------------------
# Missions
# ---------------------------------------------------------------------------

@dataclass
class Mission:
    name: str
    vehicle: str
    date: str
    outcome: str
    market_mood_pool: list[str]
    notes_pool: list[str]
    orbit: str = "LEO"
    payload: str = "unknown"

    @property
    def market_mood(self) -> str:
        return _pick(self.market_mood_pool)

    @property
    def notes(self) -> str:
        return _pick(self.notes_pool)


MISSIONS: list[Mission] = [
    Mission(
        name="Starman",
        vehicle="Falcon Heavy",
        date="2018-02-06",
        outcome="SUCCESS",
        orbit="Heliocentric",
        payload="Tesla Roadster + mannequin",
        market_mood_pool=["📈 briefly", "📈 then down", "🚀 irrational"],
        notes_pool=[
            "First Falcon Heavy flight. Starman now past Mars. Car in space. Fine.",
            "\"That's one small step for mannequin, one giant leap for automotive PR.\"",
            "FAA declined to comment on the traffic violation.",
        ],
    ),
    Mission(
        name="Crew Dragon Demo-2",
        vehicle="Falcon 9",
        date="2020-05-30",
        outcome="SUCCESS",
        orbit="ISS",
        payload="Behnken, Hurley",
        market_mood_pool=["📈 patriotism spike", "🇺🇸 NYSE up 2%", "📈 TSLA +8%"],
        notes_pool=[
            "First crewed US launch since Shuttle. NASA cried. Elon did not.",
            "Astronauts survived. NASA relief was palpable from orbit.",
            "Commercial crew era begins. Boeing watches from sideline, weeping.",
        ],
    ),
    Mission(
        name="SN8",
        vehicle="Starship",
        date="2020-12-09",
        outcome="RUD*",
        orbit="Suborbital (attempted)",
        payload="none",
        market_mood_pool=["🔥 \"nominal\"", "📉 technically", "🔥 beautiful failure"],
        notes_pool=[
            "* RUD = Rapid Unscheduled Disassembly (company terminology, not ours).",
            "Belly flop maneuver worked. Landing did not. Elon called it \"nominal\".",
            "The fireball was, objectively, very photogenic.",
        ],
    ),
    Mission(
        name="SN15",
        vehicle="Starship",
        date="2021-05-05",
        outcome="SUCCESS",
        orbit="Suborbital",
        payload="none",
        market_mood_pool=["🎉 for real this time", "📈 vindication", "🎉 SpaceX relief visible"],
        notes_pool=[
            "First Starship that landed without exploding. First time for everything.",
            "SN11 RUD'd in fog. SN12/13/14 skipped. SN15 said 'hold my propellant'.",
            "Briefly on fire after landing. Declared nominal. Tradition established.",
        ],
    ),
    Mission(
        name="Inspiration4",
        vehicle="Falcon 9",
        date="2021-09-15",
        outcome="SUCCESS",
        orbit="LEO",
        payload="4 civilians",
        market_mood_pool=["📈 retail investor approved", "💰 Netflix deal confirmed", "📈 feel-good rally"],
        notes_pool=[
            "All-civilian crew. No professional astronauts. Went fine. Suspicious.",
            "Netflix documentary. SpaceX's best PR since the Starman roadster.",
            "Dome window. Space toilet drama. Still: better than most vacations.",
        ],
    ),
    Mission(
        name="IFT-1",
        vehicle="Starship",
        date="2023-04-20",
        outcome="RUD*",
        orbit="Orbital (attempted)",
        payload="none",
        market_mood_pool=["🔥 420 jokes", "📉 FAA not amused", "🔥 debris confirmed"],
        notes_pool=[
            "Full stack first flight. 4/20. Coincidence. Maximum plausible deniability.",
            "Pad damage substantial. FAA paused. Elon tweeted anyway.",
            "Engines lit, vehicle cleared the pad, then: see outcome field.",
        ],
    ),
    Mission(
        name="IFT-2",
        vehicle="Starship",
        date="2023-11-18",
        outcome="RUD*",
        orbit="Orbital (attempted)",
        payload="none",
        market_mood_pool=["📉 slightly less on fire", "🔥 progress?", "📉 FAA sighs again"],
        notes_pool=[
            "Got further than IFT-1. That is the bar. It clears it.",
            "Stage separation achieved. Then: nominal disassembly.",
            "Hot staging ring survived longer than expected. Progress.",
        ],
    ),
    Mission(
        name="IFT-3",
        vehicle="Starship",
        date="2024-03-14",
        outcome="PARTIAL",
        orbit="Suborbital",
        payload="none",
        market_mood_pool=["📈 Pi Day launch", "🥧 nerds noticed", "📈 trending on X"],
        notes_pool=[
            "Pi Day. Intentional? SpaceX says no. We believe them at 3.14% confidence.",
            "Reached space. Reentry plasma. Lost signal. Win-ish.",
            "First Starship to reach space. Also first to dissolve on reentry. Progress.",
        ],
    ),
    Mission(
        name="IFT-4",
        vehicle="Starship",
        date="2024-06-06",
        outcome="SUCCESS",
        orbit="Suborbital",
        payload="none",
        market_mood_pool=["🎉 D-Day launch", "📈 historic timing", "🎉 actually worked"],
        notes_pool=[
            "Soft splashdown in Indian Ocean. Both vehicles survived. Unprecedented.",
            "Booster splashed. Ship splashed. Nobody exploded. Historic.",
            "IFT-1 through 3 were prologue. IFT-4 was the actual chapter.",
        ],
    ),
    Mission(
        name="IFT-5",
        vehicle="Starship",
        date="2024-10-13",
        outcome="SUCCESS",
        orbit="Suborbital",
        payload="none",
        market_mood_pool=["🦾 Mechazilla works", "📈 catch confirmed", "🦾 jaws dropped"],
        notes_pool=[
            "Mechazilla caught the booster with chopstick arms. As designed. Somehow.",
            "\"It caught it with the arms.\" — everyone, repeatedly, for weeks.",
            "OSHA was not consulted. The booster didn't care.",
        ],
    ),
    Mission(
        name="IFT-6",
        vehicle="Starship",
        date="2025-01-16",
        outcome="PARTIAL",
        orbit="Suborbital",
        payload="none",
        market_mood_pool=["📈 close enough", "😬 ship lost again", "📈 booster caught tho"],
        notes_pool=[
            "Booster caught again. Ship lost during reentry. 1.5/2. Partial credit.",
            "Debris over Caribbean. FAA investigation. SpaceX: 'data rich event'.",
            "Ship plasma'd out. Booster nailed the catch. Progress is asymmetric.",
        ],
    ),
    Mission(
        name="DOGE-1",
        vehicle="Falcon 9",
        date="2021-06-30",
        outcome="CANCELLED",
        orbit="Lunar",
        payload="DOGE meme payload",
        market_mood_pool=["🐕 to the moon (cancelled)", "📉 DOGE coin down 40%", "🐕 crypto weeps"],
        notes_pool=[
            "First crypto-funded space mission. Cancelled. Irony: maximal.",
            "Geometric Digibyte Corp paid in DOGE. Mission never launched. Perfect.",
            "DOGE-1 joins the great tradition of press releases that time forgot.",
        ],
    ),
    Mission(
        name="CRS-25",
        vehicle="Falcon 9",
        date="2022-07-15",
        outcome="SUCCESS",
        orbit="ISS",
        payload="cargo, experiments",
        market_mood_pool=["📦 routine", "📈 reliable", "📦 as expected"],
        notes_pool=[
            "Cargo resupply. Docked. Delivered. Returned. NASA was pleased.",
            "Routine excellence. The unremarkable kind that keeps astronauts alive.",
            "CRS missions: the unglamorous backbone of ISS operations.",
        ],
    ),
    Mission(
        name="Starlink Group 6-1",
        vehicle="Falcon 9",
        date="2023-05-19",
        outcome="SUCCESS",
        orbit="LEO",
        payload="21 Starlink satellites",
        market_mood_pool=["📡 more satellites", "📉 astronomers sad", "📡 Musk: 'lol'"],
        notes_pool=[
            "Another batch. Sky increasingly Musk-branded. Astronomers continue objecting.",
            "21 satellites. LEO. Operational within days. Constellation grows.",
            "Falcon 9 launch #200-something. Routine. Concerning. Both.",
        ],
    ),
]


# ---------------------------------------------------------------------------
# Vehicles
# ---------------------------------------------------------------------------

@dataclass
class Vehicle:
    name: str
    status: str
    first_flight: str
    flights: int
    payload_leo_kg: int
    reusable: bool
    notes_pool: list[str]
    systems: dict[str, str] = field(default_factory=dict)

    @property
    def notes(self) -> str:
        return _pick(self.notes_pool)


VEHICLES: list[Vehicle] = [
    Vehicle(
        name="Falcon 9",
        status="ACTIVE",
        first_flight="2010-06-04",
        flights=300,
        payload_leo_kg=22_800,
        reusable=True,
        notes_pool=[
            "World's most flight-proven orbital rocket. Boring by design.",
            "300+ flights. Costs less per kg than your last AWS bill.",
            "Booster B1058: 20 flights. Refuses to retire. SpaceX's golden goose.",
        ],
        systems={
            "propulsion": "Merlin 1D x9 (stage 1), Merlin Vacuum x1 (stage 2)",
            "guidance": "flight computer + GPS/IMU",
            "recovery": "grid fins, cold gas thrusters, landing legs",
            "comms": "S-band telemetry, C-band radar",
        },
    ),
    Vehicle(
        name="Falcon Heavy",
        status="ACTIVE",
        first_flight="2018-02-06",
        flights=9,
        payload_leo_kg=63_800,
        reusable=True,
        notes_pool=[
            "3 Falcon 9 cores duct-taped together. Elon's words (paraphrased).",
            "Most powerful operational rocket. Used mainly for national security.",
            "Side boosters land simultaneously. Crowd goes feral every time.",
        ],
        systems={
            "propulsion": "Merlin 1D x27 + Merlin Vacuum x1",
            "guidance": "triple-core synchronized flight computer",
            "recovery": "two side core boosters, center core optional",
            "comms": "S-band, Ka-band payload uplink",
        },
    ),
    Vehicle(
        name="Starship",
        status="TESTING",
        first_flight="2023-04-20",
        flights=6,
        payload_leo_kg=150_000,
        reusable=True,
        notes_pool=[
            "Largest rocket ever flown. 6 flights. 3 explosions. Trajectory: improving.",
            "33 Raptor engines. Mechazilla catch system. Fully reusable. Eventually.",
            "Elon says: point-to-point Earth travel. Engineers say: first survive reentry.",
        ],
        systems={
            "propulsion": "Raptor 2 x33 (booster), Raptor 2 x6 (ship)",
            "guidance": "autonomous flight + ground-assist catch",
            "recovery": "Mechazilla tower catch (booster), controlled reentry (ship)",
            "comms": "Starlink relay, SpaceX ground network",
            "heat_shield": "hexagonal ceramic tiles, ~18,000 pieces",
        },
    ),
    Vehicle(
        name="Dragon",
        status="ACTIVE",
        first_flight="2010-12-08",
        flights=45,
        payload_leo_kg=6_000,
        reusable=True,
        notes_pool=[
            "NASA's taxi of choice. Replaced Soyuz for US crews. Boeing still working on it.",
            "Cargo and crew variants. Crew Dragon has a cupola. Soyuz did not.",
            "Splashes down in Atlantic. Coast Guard presence required. Tradition.",
        ],
        systems={
            "propulsion": "SuperDraco abort x8, Draco thrusters x16",
            "guidance": "autonomous rendezvous, NASA-verified docking",
            "recovery": "parachute system x4, ocean splashdown",
            "life_support": "ECLSS for up to 7 crew (4 nominal)",
        },
    ),
    Vehicle(
        name="Mechazilla",
        status="ACTIVE",
        first_flight="2024-10-13",
        flights=2,
        payload_leo_kg=0,
        reusable=True,
        notes_pool=[
            "Not a rocket. A 145m tower with arms that catches 70m rockets. Casual.",
            "Mechazilla is the catch system. Also the nickname. SpaceX approved both.",
            "\"The robot arms\" per most headlines. 'Mechazilla' per anyone cool.",
        ],
        systems={
            "arms": "Mechazilla chopsticks: hydraulic, 20m span",
            "structure": "145m steel tower, Boca Chica TX",
            "catch_guidance": "optical + radar terminal guidance",
            "propellant": "methane + LOX loading tower integrated",
        },
    ),
]


# ---------------------------------------------------------------------------
# Competitors
# ---------------------------------------------------------------------------

@dataclass
class Competitor:
    name: str
    founded: int
    status: str
    tagline: str
    rockets: list[str]
    crewed: bool
    notable_failure: str
    metrics: dict[str, str]


COMPETITORS: list[Competitor] = [
    Competitor(
        name="Boeing",
        founded=1916,
        status="STRUGGLING",
        tagline="We used to build good things.",
        rockets=["Starliner (eventually)"],
        crewed=True,
        notable_failure="Starliner: crewed test flight left astronauts stranded 8 months",
        metrics={
            "reliability": "aspirational",
            "cost_per_kg": "$130,000+ (Starliner estimate)",
            "reusability": "no",
            "government_contracts": "yes, many, despite everything",
            "astronaut_confidence": "publicly high, privately: see Starliner incident",
        },
    ),
    Competitor(
        name="Blue Origin",
        founded=2000,
        status="ACTIVE",
        tagline="Gradatim ferociter. (Step by step, ferociously. Very step. Much ferocious.)",
        rockets=["New Shepard", "New Glenn"],
        crewed=True,
        notable_failure="New Glenn's debut partial failure; New Shepard anomaly 2022",
        metrics={
            "reliability": "improving",
            "cost_per_kg": "undisclosed (a theme)",
            "reusability": "yes (New Shepard), partial (New Glenn)",
            "bezos_involvement": "\"founder\", now board member",
            "launch_cadence": "SpaceX does more before 9am than BO does all quarter",
        },
    ),
    Competitor(
        name="Virgin Galactic",
        founded=2004,
        status="SUSPENDED",
        tagline="Space for the wealthy. Literally.",
        rockets=["VSS Unity (retired)", "VSS Imagine (not yet)"],
        crewed=True,
        notable_failure="VSS Enterprise fatal crash 2014; Unity retired 2023",
        metrics={
            "altitude": "~85km (US definition of space; FAI disagrees)",
            "ticket_price": "$450,000",
            "flights_completed": "handful",
            "branson_tweet_count": "immeasurable",
            "current_status": "suspended ops; Delta class vehicle: TBD",
        },
    ),
    Competitor(
        name="ULA",
        founded=2006,
        status="ACTIVE",
        tagline="Government-grade reliability at government-grade prices.",
        rockets=["Atlas V (retiring)", "Vulcan Centaur"],
        crewed=False,
        notable_failure="Atlas V RD-180 Russian engine dependency during sanctions era",
        metrics={
            "reliability": "100% success rate (Atlas V)",
            "cost_per_kg": "~$20,000+ (Vulcan estimate)",
            "reusability": "no (Vulcan: partial SMART Reuse planned)",
            "launch_cadence": "~6/year",
            "government_lock_in": "historic; Bezos now a customer",
        },
    ),
    Competitor(
        name="Roscosmos",
        founded=1992,
        status="DECLINING",
        tagline="Once we put Gagarin in space. That was nice.",
        rockets=["Soyuz", "Proton (retiring)", "Angara A5"],
        crewed=True,
        notable_failure="Numerous: Proton anomalies, Soyuz abort 2018, sanction isolation",
        metrics={
            "heritage": "Sputnik, Gagarin, Mir, ISS — genuinely impressive",
            "current_trajectory": "downward since 2022 sanctions",
            "soyuz_reliability": "historically excellent; recently: see 2018",
            "export_market": "effectively zero post-Ukraine invasion",
            "nasa_dependence": "ended 2020 (Crew Dragon). They noticed.",
        },
    ),
]


# ---------------------------------------------------------------------------
# Daemons
# ---------------------------------------------------------------------------

@dataclass
class Daemon:
    id: str
    name: str
    status: str
    description: str
    started: str
    refs: list[str]
    notes_pool: list[str]

    @property
    def notes(self) -> str:
        return _pick(self.notes_pool)


DAEMONS: list[Daemon] = [
    Daemon(
        id="funding-secured",
        name="funding-secured",
        status="RUNNING",
        description="SEC investigation re: 2018 'funding secured' tweet. $420M, 420/share.",
        started="2018-08-07",
        refs=["SEC v. Musk (2018)", "NYT: 'Elon Musk Settles SEC Fraud Charges'",
              "Reuters: 'Musk pays $20M fine, steps down as Tesla chairman'"],
        notes_pool=[
            "Settled for $20M + chairman resign. Did not stop tweeting. Lesson: unclear.",
            "420. The number. On purpose? SEC thought so. Jury (literally) still out.",
            "'Am considering taking Tesla private at $420' — tweet that cost $40M combined.",
        ],
    ),
    Daemon(
        id="twitter-acquisition-chaos",
        name="twitter-acquisition-chaos",
        status="RUNNING",
        description="$44B Twitter acquisition fallout: mass layoffs, advertiser exodus, rebranding to X.",
        started="2022-10-27",
        refs=["WSJ: 'Elon Musk Completes Twitter Takeover'",
              "The Verge: 'Twitter is X now'",
              "Bloomberg: 'Twitter Revenue Down 50% Post-Acquisition'"],
        notes_pool=[
            "$44B. Half the advertisers left. Rebranded to X. The bird is gone.",
            "Laid off ~80% of staff. Service still mostly works. Twitter engineers haunted.",
            "\"The bird is freed.\" — Elon. Bird logo: retired. Freedom: debated.",
        ],
    ),
    Daemon(
        id="doge-conflict-of-interest",
        name="doge-conflict-of-interest",
        status="RUNNING",
        description="DOGE advisory role conflicts with SpaceX/Tesla govt contracts. Watchdogs watching.",
        started="2025-01-20",
        refs=["ProPublica: 'Musk's DOGE Role and His Federal Contracts'",
              "NYT: 'The Conflict at the Heart of DOGE'",
              "WaPo: 'Musk's Expanding Government Footprint'"],
        notes_pool=[
            "DOGE cuts agencies that regulate SpaceX. Coincidence: <3% confidence.",
            "SpaceX contracts: $15B+. DOGE budget authority: TBD. Ethics: also TBD.",
            "Watchdog groups filed suits. Courts: various. Resolution: pending.",
        ],
    ),
    Daemon(
        id="starship-faa-delays",
        name="starship-faa-delays",
        status="RUNNING",
        description="FAA environmental review + license delays for Starship launches at Boca Chica.",
        started="2021-09-01",
        refs=["Reuters: 'FAA Delays SpaceX Starship Launch License'",
              "Ars Technica: 'The FAA vs Starship: A timeline'",
              "The Guardian: 'SpaceX Boca Chica Environmental Impact'"],
        notes_pool=[
            "IFT-1 delayed ~7 months for FAA review. Elon tweeted through it.",
            "75-point corrective action plan. SpaceX completed it. FAA: 'good, more forms'.",
            "FAA: protecting public. SpaceX: protecting schedule. Tension: ongoing.",
        ],
    ),
    Daemon(
        id="tesla-autopilot-investigations",
        name="tesla-autopilot-investigations",
        status="RUNNING",
        description="NHTSA/DOJ probes into Autopilot/FSD crashes, recall, and marketing claims.",
        started="2021-08-16",
        refs=["NHTSA: 'Tesla Autopilot Investigation ODI PE21006'",
              "WaPo: 'Tesla's Self-Driving Claims Under Federal Scrutiny'",
              "NYT: 'Tesla Recalls 2M Vehicles Over Autopilot Safety'"],
        notes_pool=[
            "2M vehicle recall. 'Autopilot' does not drive itself. NHTSA noted this.",
            "DOJ subpoena issued. 'Full Self-Driving' still in beta. Naming: generous.",
            "400+ crashes investigated. Tesla: 'driver misuse'. NHTSA: 'investigating'.",
        ],
    ),
    Daemon(
        id="spacex-settlement-nlrb",
        name="spacex-settlement-nlrb",
        status="RUNNING",
        description="NLRB complaint: SpaceX fired employees who circulated open letter criticizing Musk.",
        started="2022-06-16",
        refs=["NLRB Complaint: SpaceX Case 31-CA-290646",
              "Bloomberg: 'SpaceX Faces Labor Board Complaint Over Employee Firings'",
              "Reuters: 'SpaceX fired workers who wrote letter about Musk conduct'"],
        notes_pool=[
            "8 employees fired after open letter re: Musk conduct. NLRB called it illegal.",
            "Letter asked SpaceX leadership to distance from Musk's tweets. Then: fired.",
            "SpaceX contested NLRB jurisdiction. Courts: still deciding. Workers: still fired.",
        ],
    ),
    Daemon(
        id="neuralink-animal-welfare",
        name="neuralink-animal-welfare",
        status="RUNNING",
        description="Neuralink animal testing welfare concerns; FDA approval process scrutiny.",
        started="2022-12-05",
        refs=["Reuters: 'Neuralink Faces Federal Probe for Potential Animal-Welfare Violations'",
              "WaPo: 'Inside Neuralink's Animal Testing Program'"],
        notes_pool=[
            "1,500+ animals dead during testing per Reuters. FDA probe ongoing.",
            "USDA probe opened 2022. Neuralink: 'compliant'. Reuters: 'here are the documents'.",
            "First human implant: Jan 2024. Animal program: still under investigation.",
        ],
    ),
    Daemon(
        id="sec-vs-elon-twitter-poll",
        name="sec-vs-elon-twitter-poll",
        status="RUNNING",
        description="SEC subpoena re: Musk's 2021 Twitter poll on Tesla stock sale.",
        started="2021-11-06",
        refs=["SEC Subpoena: In the Matter of Elon Musk (2022)",
              "Bloomberg: 'SEC Investigating Musk's Tesla Stock Poll Tweet'",
              "NYT: 'Elon Musk and the SEC's Long-Running Feud'"],
        notes_pool=[
            "Poll: 'Should I sell 10% of Tesla stock?' 58% yes. SEC: 'we have questions'.",
            "Musk sold $8B in Tesla after the poll. SEC subpoena followed. Timing noted.",
            "Court ordered Musk to comply with SEC subpoena. Musk appealed. Ongoing.",
        ],
    ),
]


# ---------------------------------------------------------------------------
# Lookup helpers
# ---------------------------------------------------------------------------

def find_mission(name: str) -> Optional[Mission]:
    name_lower = name.lower()
    for m in MISSIONS:
        if m.name.lower() == name_lower:
            return m
    # fuzzy: prefix match
    for m in MISSIONS:
        if m.name.lower().startswith(name_lower):
            return m
    return None


def find_vehicle(name: str) -> Optional[Vehicle]:
    name_lower = name.lower()
    for v in VEHICLES:
        if v.name.lower() == name_lower:
            return v
    for v in VEHICLES:
        if v.name.lower().startswith(name_lower):
            return v
    return None


def find_competitor(name: str) -> Optional[Competitor]:
    name_lower = name.lower()
    for c in COMPETITORS:
        if c.name.lower() == name_lower:
            return c
    for c in COMPETITORS:
        if c.name.lower().startswith(name_lower):
            return c
    return None


def find_daemon(id_or_name: str) -> Optional[Daemon]:
    key = id_or_name.lower()
    for d in DAEMONS:
        if d.id == key or d.name.lower() == key:
            return d
    return None
