-- DEMO / SIMULATED DATA.
-- Three fictional demo routes (D1-D3) through central Baku. Stop names refer to
-- well-known places and coordinates are approximate. These are NOT official
-- BakuBus routes or timetables. Every route is flagged is_simulated = TRUE.

INSERT INTO stops (id, name, latitude, longitude) VALUES
    ('stop_icherisheher', 'Icherisheher',        40.3658, 49.8314),
    ('stop_sahil',        'Sahil',               40.3717, 49.8443),
    ('stop_28may',        '28 May',              40.3797, 49.8486),
    ('stop_ganjlik',      'Ganjlik',             40.4003, 49.8516),
    ('stop_narimanov',    'Narimanov',           40.4028, 49.8705),
    ('stop_ulduz',        'Ulduz',               40.4149, 49.8915),
    ('stop_koroglu',      'Koroglu',             40.4209, 49.9183),
    ('stop_nizami',       'Nizami',              40.3792, 49.8301),
    ('stop_elmlar',       'Elmlar Akademiyasi',  40.3750, 49.8155),
    ('stop_inshaatchilar','Inshaatchilar',       40.3803, 49.8027),
    ('stop_20yanvar',     '20 Yanvar',           40.4040, 49.8085),
    ('stop_memarajami',   'Memar Ajami',         40.4105, 49.8130),
    ('stop_nasimi',       'Nasimi',              40.4245, 49.8240),
    ('stop_azadliq',      'Azadliq prospekti',   40.4258, 49.8427),
    ('stop_darnagul',     'Darnagul',            40.4251, 49.8616),
    ('stop_khatai',       'Khatai',              40.3833, 49.8717);

INSERT INTO routes (id, route_number, name, is_simulated) VALUES
    ('route_d1', 'D1', 'Icherisheher - Koroglu (demo)',       TRUE),
    ('route_d2', 'D2', 'Icherisheher - Darnagul (demo)',      TRUE),
    ('route_d3', 'D3', 'Inshaatchilar - Khatai (demo)',       TRUE);

-- Direction 0 (outbound) is listed explicitly; direction 1 (return) is derived below.
INSERT INTO route_stops (route_id, stop_id, direction, stop_order) VALUES
    ('route_d1', 'stop_icherisheher', 0, 0),
    ('route_d1', 'stop_sahil',        0, 1),
    ('route_d1', 'stop_28may',        0, 2),
    ('route_d1', 'stop_ganjlik',      0, 3),
    ('route_d1', 'stop_narimanov',    0, 4),
    ('route_d1', 'stop_ulduz',        0, 5),
    ('route_d1', 'stop_koroglu',      0, 6),

    ('route_d2', 'stop_icherisheher', 0, 0),
    ('route_d2', 'stop_nizami',       0, 1),
    ('route_d2', 'stop_elmlar',       0, 2),
    ('route_d2', 'stop_20yanvar',     0, 3),
    ('route_d2', 'stop_memarajami',   0, 4),
    ('route_d2', 'stop_nasimi',       0, 5),
    ('route_d2', 'stop_azadliq',      0, 6),
    ('route_d2', 'stop_darnagul',     0, 7),

    ('route_d3', 'stop_inshaatchilar',0, 0),
    ('route_d3', 'stop_elmlar',       0, 1),
    ('route_d3', 'stop_nizami',       0, 2),
    ('route_d3', 'stop_28may',        0, 3),
    ('route_d3', 'stop_khatai',       0, 4);

-- Direction 1: the same stops in reverse order.
INSERT INTO route_stops (route_id, stop_id, direction, stop_order)
SELECT rs.route_id, rs.stop_id, 1, m.max_order - rs.stop_order
FROM route_stops rs
JOIN (SELECT route_id, max(stop_order) AS max_order
      FROM route_stops WHERE direction = 0 GROUP BY route_id) m USING (route_id)
WHERE rs.direction = 0;

-- Route geometry: straight segments between consecutive stops (good enough for
-- the simulator and map until real shapes are available).
UPDATE routes r SET geometry = g.geom
FROM (
    SELECT rs.route_id,
           jsonb_agg(jsonb_build_array(s.latitude, s.longitude) ORDER BY rs.stop_order) AS geom
    FROM route_stops rs
    JOIN stops s ON s.id = rs.stop_id
    WHERE rs.direction = 0
    GROUP BY rs.route_id
) g
WHERE r.id = g.route_id;

INSERT INTO vehicles (id, label) VALUES
    ('bus_101', 'Demo bus 101'),
    ('bus_102', 'Demo bus 102'),
    ('bus_201', 'Demo bus 201'),
    ('bus_202', 'Demo bus 202'),
    ('bus_301', 'Demo bus 301');

-- Initial simulated trips. The simulator turns each bus around at the end of
-- its route, completing the trip and starting one in the other direction.
INSERT INTO trips (route_id, vehicle_id, direction, status) VALUES
    ('route_d1', 'bus_101', 0, 'IN_PROGRESS'),
    ('route_d1', 'bus_102', 1, 'IN_PROGRESS'),
    ('route_d2', 'bus_201', 0, 'IN_PROGRESS'),
    ('route_d2', 'bus_202', 1, 'IN_PROGRESS'),
    ('route_d3', 'bus_301', 0, 'IN_PROGRESS');
