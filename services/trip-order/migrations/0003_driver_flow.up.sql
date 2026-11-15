-- Haydovchi oqimi: "yetib keldim" (arrived), taklifni rad etish, safarni bekor qilish (buyurtma qayta qidiruvga qaytadi)
-- va admin bekor qilishi.

-- buyurtma holatlari: arrived qo'shiladi
ALTER TABLE orders DROP CONSTRAINT orders_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_status_check
    CHECK (status IN ('searching', 'accepted', 'arrived', 'in_progress', 'completed', 'cancelled', 'expired'));
ALTER TABLE orders ADD COLUMN cancelled_by text CHECK (cancelled_by IN ('passenger', 'admin', 'system'));

-- taklif: rad etilgan holat va sabab
ALTER TABLE offers DROP CONSTRAINT offers_status_check;
ALTER TABLE offers ADD CONSTRAINT offers_status_check CHECK (status IN ('pending', 'accepted', 'closed', 'rejected'));
ALTER TABLE offers ADD COLUMN reject_reason text CHECK (reject_reason IN ('too_far', 'bad_price', 'busy', 'other'));

-- safar: yetib kelish va bekor qilish
ALTER TABLE trips
    ADD COLUMN arrived_at    timestamptz,
    ADD COLUMN cancelled_at  timestamptz,
    ADD COLUMN cancelled_by  text CHECK (cancelled_by IN ('driver', 'admin')),
    ADD COLUMN cancel_reason text;

-- bekor qilingan safardan keyin buyurtma boshqa haydovchiga o'tishi mumkin: bitta buyurtmaga bitta FAOL safar
ALTER TABLE trips DROP CONSTRAINT trips_order_id_key;
CREATE UNIQUE INDEX trips_order_live_idx ON trips (order_id) WHERE status <> 'cancelled';
CREATE INDEX trips_driver_history_idx ON trips (driver_id, created_at DESC, id DESC);
