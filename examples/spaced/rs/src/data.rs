//! Demo data: the subset of the Go demo's archive the parity harness
//! reads. Names, dates and outcomes mirror `examples/spaced/go/data`.

pub struct Mission {
    pub name: &'static str,
    pub vehicle: &'static str,
    pub date: &'static str,
    pub orbit: &'static str,
    pub payload: &'static str,
    pub outcome: &'static str,
    pub note: &'static str,
}

pub const MISSIONS: [Mission; 3] = [
    Mission {
        name: "Starman",
        vehicle: "Falcon Heavy",
        date: "2018-02-06",
        orbit: "Heliocentric",
        payload: "Tesla Roadster + Starman mannequin",
        outcome: "SUCCESS",
        note: "Currently past Mars. Not coming back for the paperwork.",
    },
    Mission {
        name: "SN8",
        vehicle: "Starship SN8",
        date: "2020-12-09",
        orbit: "Suborbital",
        payload: "Ambition",
        outcome: "RUD",
        note: "Landing burn nominal until the landing part.",
    },
    Mission {
        name: "Crew Dragon Demo2",
        vehicle: "Falcon 9",
        date: "2020-05-30",
        orbit: "LEO",
        payload: "2 NASA astronauts",
        outcome: "SUCCESS",
        note: "First crewed orbital flight from US soil since 2011.",
    },
];

pub fn find_mission(name: &str) -> Option<&'static Mission> {
    MISSIONS.iter().find(|m| m.name.eq_ignore_ascii_case(name))
}

pub struct System {
    pub name: &'static str,
    pub status: &'static str,
    pub description: &'static str,
}

pub struct Vehicle {
    pub name: &'static str,
    pub kind: &'static str,
    pub status: &'static str,
    pub first_flight: &'static str,
    pub systems: [System; 2],
}

pub const VEHICLES: [Vehicle; 2] = [
    Vehicle {
        name: "Falcon 9",
        kind: "Orbital rocket",
        status: "ACTIVE",
        first_flight: "2010-06-04",
        systems: [
            System {
                name: "propulsion",
                status: "NOMINAL",
                description: "Nine Merlin 1D engines, reused until they are not",
            },
            System {
                name: "landing",
                status: "NOMINAL",
                description: "Grid fins and four landing legs; lands on a drone ship",
            },
        ],
    },
    Vehicle {
        name: "Starship",
        kind: "Super heavy-lift",
        status: "TESTING",
        first_flight: "2023-04-20",
        systems: [
            System {
                name: "propulsion",
                status: "EVOLVING",
                description: "Raptor engines; the count changes with the revision",
            },
            System {
                name: "landing",
                status: "CAUGHT",
                description: "Chopsticks. Yes, really",
            },
        ],
    },
];

pub fn find_vehicle(name: &str) -> Option<&'static Vehicle> {
    VEHICLES.iter().find(|v| v.name.eq_ignore_ascii_case(name))
}

pub struct Daemon {
    pub name: &'static str,
    pub status: &'static str,
    pub since: &'static str,
    pub summary: &'static str,
    pub references: [&'static str; 2],
}

pub const DAEMONS: [Daemon; 2] = [
    Daemon {
        name: "funding-secured",
        status: "RUNNING",
        since: "2018-08-07",
        summary: "A tweet about taking a company private at a price.",
        references: [
            "SEC - complaint, 2018-09-27",
            "The New York Times - 2018-08-07",
        ],
    },
    Daemon {
        name: "starship-faa-delays",
        status: "RUNNING",
        since: "2023-04-20",
        summary: "Licensing paperwork meets an engineering schedule.",
        references: [
            "SEC - filing, 2023-05-01",
            "The New York Times - 2023-04-21",
        ],
    },
];

pub fn find_daemon(id: &str) -> Option<&'static Daemon> {
    DAEMONS.iter().find(|d| d.name.eq_ignore_ascii_case(id))
}
