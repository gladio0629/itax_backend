-- OCHIQ: narx formulasi va koeffitsientlar hujjatda hali tasdiqlanmagan — quyidagilar TAKLIF (boshlang'ich) qiymatlar.
-- Backoffice tasdiqlangan tarifni qo'shib, shuni almashtiradi. Pul tiyinda, ko'paytirgichlar promilleda.
-- min_fare=0 va rounding_step=1 — vazifadagi formulani aynan takrorlaydi; kerak bo'lsa tarifda yoqiladi.
INSERT INTO tariffs (id, version, active, base_fare, per_km_rate, seat_multiplier,
                     car_class_multipliers, vehicle_multipliers, fuel_surcharge,
                     min_fare, rounding_step, commission_bps, activated_at)
VALUES (
    '00000000-0000-7000-8000-000000000001', 1, true,
    400000,   -- base_fare: 4 000 so'm
    120000,   -- per_km_rate: 1 200 so'm/km
    1000,     -- seat_multiplier: 1.0 (omil = o'rindiqlar soni)
    '{"economy": 4000, "comfort": 5000, "business": 6400, "minivan": 6000}',
    '{"spark": 1000, "damas": 1300, "labo": 1500, "isuzu": 2000, "isuzu2": 2500, "fura": 3000}',
    0,        -- fuel_surcharge: 0%
    0,        -- min_fare: yo'q (formula: baseFare + masofa × ...)
    1,        -- rounding_step: yaxlitlanmaydi (narx butun tiyin)
    1000,     -- commission_bps: 10%
    now()
);
