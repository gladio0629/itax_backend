-- Operator email'siz bo'lishi mumkin: u admin panelga faqat telefon + SMS bilan kiradi.
ALTER TABLE staff_users ALTER COLUMN email DROP NOT NULL;

-- Email'siz kirish (email qadamisiz) faqat operatorga ruxsat: boshqa rollar uchun email majburiy,
-- aks holda email omilini chetlab o'tib bo'lardi.
ALTER TABLE staff_users ADD CONSTRAINT staff_users_email_required
    CHECK (email IS NOT NULL OR role = 'operator');

-- Telefon bo'yicha kirish xodimni bir ma'noli topishi kerak.
CREATE UNIQUE INDEX staff_users_phone_key ON staff_users (phone);
