package main

import (
	"strings"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// seedRichDemo наполняет базу щедрым выдуманным датасетом: все экраны получают
// много разнообразных строк, чтобы вёрстку и агрегаты можно было смотреть на
// «живых» данных, а не на двух заглушках. Рассчитан на запуск с -empty -demo;
// на копии реальной базы строки с занятыми ключами (telegram_id, коды инвайтов)
// молча пропускаются (INSERT OR IGNORE), остальные — Warn в лог.
//
// Датасет покрывает: 3 ноды (вкл. выключенную), товары в двух валютах,
// 24 подписки со всеми статусами и разбросом сроков/масок напоминаний,
// привязки узлов во всех состояниях (вкл. застрявшие pending_remove с
// ретраями), оплаты за 45 дней (гистограмма выручки + «несколько валют»),
// 4 рассылки с полным delivery_report, триал-запросы с выраженным топ-IP,
// инвайты и журнал аудита.
func seedRichDemo(store *database.Service) {
	db := store.GetDB()

	statements := []string{
		// Пустая база содержит только встроенные trial/free — премиум создаём,
		// если его нет: на всё дальше ссылаемся по имени.
		`INSERT OR IGNORE INTO plans (name, devices_limit, traffic_limit, is_active)
		 VALUES ('premium', 3, 0, 1)`,

		// --- Ноды: две активные, одна выключенная ---
		`INSERT OR IGNORE INTO nodes (id, name, is_active, host, subscription_url, type, inbound_ids)
		 VALUES (1, 'demo-fra', 1, 'https://demo-fra.example.test/pnl', 'https://demo-fra.example.test/sub', '3x-ui', '[1,2]'),
		        (2, 'demo-ams', 1, 'https://demo-ams.example.test/pnl', 'https://demo-ams.example.test/sub', '3x-ui', '[7]'),
		        (3, 'demo-old', 0, 'https://demo-old.example.test/pnl', 'https://demo-old.example.test/sub', '3x-ui', '[3]')`,

		// --- Товары: премиум в рублях (4 срока) + пробник в долларах ---
		`INSERT OR IGNORE INTO products (id, plan_id, name, duration_days, price_cents, currency, is_active)
		 VALUES (1, (SELECT id FROM plans WHERE name='premium'), 'Premium на 30 дней', 30, 29900, 'RUB', 1),
		        (2, (SELECT id FROM plans WHERE name='premium'), 'Premium на 90 дней', 90, 79900, 'RUB', 1),
		        (3, (SELECT id FROM plans WHERE name='premium'), 'Premium на полгода', 180, 149900, 'RUB', 1),
		        (4, (SELECT id FROM plans WHERE name='premium'), 'Premium на год', 360, 299900, 'RUB', 1),
		        (5, (SELECT id FROM plans WHERE name='premium'), 'Premium trial (USD)', 45, 499, 'USD', 1)`,

		// --- Привязка планов к нодам ---
		`INSERT OR IGNORE INTO plan_nodes (plan_id, node_id)
		 SELECT p.id, n.id FROM plans p, nodes n WHERE n.id <= 2 AND p.name IN ('premium','free')`,

		// --- Подписки: все статусы, разброс сроков и масок напоминаний ---
		// Истекающие близко (для «Истекают» и чипов напоминаний).
		`INSERT OR IGNORE INTO subscriptions
		 (id, telegram_id, username, client_id, subscription_id, status, plan_id, created_at, started_at, expires_at, last_request, price_paid_cents, currency, reminders_sent, traffic_reminders_sent, devices, ips)
		 VALUES
		 (101, 991001, 'ivan_demo',   'c-101', 'sub-101', 'active', (SELECT id FROM plans WHERE name='premium'), datetime('now','-40 days'), datetime('now','-40 days'), datetime('now','+2 hours'),  datetime('now','-10 minutes'), 29900, 'RUB', 4, 0, '["Windows","iPhone"]', '["91.108.4.1"]'),
		 (102, 991002, 'maria_demo',  'c-102', 'sub-102', 'active', (SELECT id FROM plans WHERE name='premium'), datetime('now','-28 days'), datetime('now','-28 days'), datetime('now','+1 day'),    datetime('now','-1 hours'),   29900, 'RUB', 3, 1, '["MacBook"]', '["91.108.4.2"]'),
		 (103, 991003, 'oleg_demo',   'c-103', 'sub-103', 'active', (SELECT id FROM plans WHERE name='premium'), datetime('now','-27 days'), datetime('now','-27 days'), datetime('now','+2 days'),   datetime('now','-3 hours'),   79900, 'RUB', 3, 3, '[]', '[]'),
		 (104, 991004, 'anna_demo',   'c-104', 'sub-104', 'active', (SELECT id FROM plans WHERE name='premium'), datetime('now','-25 days'), datetime('now','-25 days'), datetime('now','+5 days'),   datetime('now','-25 hours'), 29900, 'RUB', 1, 0, '["Android"]', '["91.108.4.4"]'),
		 (105, 991005, 'petr_demo',   'c-105', 'sub-105', 'active', (SELECT id FROM plans WHERE name='free'),    datetime('now','-60 days'), datetime('now','-60 days'), datetime('now','+10 days'),  datetime('now','-2 days'),        0, 'RUB', 0, 0, '[]', '[]'),
		 (106, 991006, 'sveta_demo',  'c-106', 'sub-106', 'active', (SELECT id FROM plans WHERE name='premium'), datetime('now','-90 days'), datetime('now','-90 days'), datetime('now','+24 days'),  datetime('now','-6 hours'),  149900, 'RUB', 0, 0, '["iPhone","iPad"]', '["91.108.4.6","91.108.4.7"]'),
		 (107, 991007, 'dmitry_demo', 'c-107', 'sub-107', 'active', (SELECT id FROM plans WHERE name='premium'), datetime('now','-100 days'), datetime('now','-100 days'), datetime('now','+60 days'), datetime('now','-30 minutes'), 299900, 'RUB', 0, 0, '[]', '[]'),
		 (108, 991008, 'kate_demo',   'c-108', 'sub-108', 'active', (SELECT id FROM plans WHERE name='premium'), datetime('now','-5 days'), datetime('now','-5 days'), datetime('now','+200 days'), datetime('now','-4 hours'), 29900, 'RUB', 0, 0, '["Linux"]', '["91.108.4.8"]'),
		 (109, 991009, 'sergey_demo', 'c-109', 'sub-109', 'active', (SELECT id FROM plans WHERE name='trial'),   datetime('now','-1 days'), datetime('now','-1 days'), datetime('now','+2 days'),   NULL, 0, 'RUB', 0, 0, '[]', '[]'),
		 (110, 991010, 'lena_demo',   'c-110', 'sub-110', 'active', (SELECT id FROM plans WHERE name='trial'),   datetime('now','-2 days'), datetime('now','-2 days'), datetime('now','+1 hours'),  NULL, 0, 'RUB', 0, 0, '[]', '[]')`,

		// Неистекающие активные + прочие статусы.
		`INSERT OR IGNORE INTO subscriptions
		 (id, telegram_id, username, client_id, subscription_id, status, plan_id, created_at, started_at, expires_at, last_request, price_paid_cents, currency, reminders_sent, traffic_reminders_sent, devices, ips, referred_by)
		 VALUES
		 (111, 991011, 'boris_demo',  'c-111', 'sub-111', 'active', (SELECT id FROM plans WHERE name='premium'), datetime('now','-200 days'), datetime('now','-200 days'), datetime('now','+330 days'), datetime('now','-5 days'), 29900, 'RUB', 0, 0, '[]', '[]', NULL),
		 (112, 991012, 'olga_demo',   'c-112', 'sub-112', 'active', (SELECT id FROM plans WHERE name='free'),    datetime('now','-150 days'), datetime('now','-150 days'), datetime('now','+40 days'),  datetime('now','-1 days'), 0, 'RUB', 0, 0, '[]', '[]', 991005),
		 (113, 991013, 'viktor_demo', 'c-113', 'sub-113', 'expired', (SELECT id FROM plans WHERE name='premium'), datetime('now','-60 days'), datetime('now','-60 days'), datetime('now','-5 days'),  datetime('now','-6 days'), 29900, 'RUB', 7, 7, '[]', '[]', NULL),
		 (114, 991014, 'nina_demo',   'c-114', 'sub-114', 'expired', (SELECT id FROM plans WHERE name='free'),    datetime('now','-120 days'), datetime('now','-120 days'), datetime('now','-20 days'), NULL, 0, 'RUB', 0, 0, '[]', '[]', NULL),
		 (115, 991015, 'grisha_demo', 'c-115', 'sub-115', 'paused', (SELECT id FROM plans WHERE name='premium'), datetime('now','-30 days'), datetime('now','-30 days'), datetime('now','+15 days'), NULL, 29900, 'RUB', 0, 0, '[]', '[]', NULL),
		 (116, 991016, 'tanya_demo',  'c-116', 'sub-116', 'canceled', (SELECT id FROM plans WHERE name='premium'), datetime('now','-45 days'), datetime('now','-45 days'), datetime('now','-2 days'), NULL, 29900, 'RUB', 0, 0, '[]', '[]', NULL),
		 (117, 991017, 'yura_demo',   'c-117', 'sub-117', 'revoked', (SELECT id FROM plans WHERE name='premium'), datetime('now','-80 days'), datetime('now','-80 days'), datetime('now','-10 days'), NULL, 29900, 'RUB', 0, 0, '[]', '[]', 991002)`,

		// --- Привязки узлов: активные, ожидание, застрявшее удаление ---
		`INSERT OR IGNORE INTO subscription_nodes (subscription_id, node_id, status, retry_count, updated_at)
		 SELECT s.id, 1, 'active', 0, datetime('now') FROM subscriptions s WHERE s.id BETWEEN 101 AND 112`,
		`INSERT OR IGNORE INTO subscription_nodes (subscription_id, node_id, status, retry_count, updated_at)
		 SELECT s.id, 2, 'active', 0, datetime('now') FROM subscriptions s WHERE s.id BETWEEN 101 AND 108`,
		`INSERT OR IGNORE INTO subscription_nodes (subscription_id, node_id, status, retry_count, retry_at, last_error, updated_at)
		 SELECT s.id, 2, 'pending_add', 2, datetime('now','-30 minutes'), 'dial tcp 203.0.113.10:443: connectex: connection refused', datetime('now','-30 minutes')
		 FROM subscriptions s WHERE s.id IN (109, 110)`,
		`INSERT OR IGNORE INTO subscription_nodes (subscription_id, node_id, status, retry_count, retry_at, last_error, updated_at)
		 SELECT s.id, 1, 'pending_remove', 3, datetime('now','-2 days'), 'node unreachable: context deadline exceeded', datetime('now','-2 days')
		 FROM subscriptions s WHERE s.id IN (113, 117)`,
		`INSERT OR IGNORE INTO subscription_nodes (subscription_id, node_id, status, retry_count, updated_at)
		 SELECT s.id, 3, 'pending_update', 0, datetime('now','-1 hours') FROM subscriptions s WHERE s.id = 111`,
	}

	for _, statement := range statements {
		execErr := db.Exec(statement).Error
		if execErr != nil {
			logger.Warn("demo seed statement failed",
				zap.String("statement", strings.SplitN(statement, "\n", 2)[0]),
				zap.Error(execErr))
		}
	}

	seedRichDemoOrders(db)
	seedRichDemoBroadcasts(db)
	seedRichDemoMisc(db)
}

// seedRichDemoOrders — оплаты за 45 дней: гистограмма выручки, средний чек,
// разброс callback_amount, вторая валюта (USD) и неоплаченные статусы.
func seedRichDemoOrders(db *gorm.DB) {
	statements := []string{
		// Оплаченные: по одному-два в день, чтобы столбики гистограммы были живые.
		`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, provider_payment_id, created_at, paid_at, activated_at, callback_amount_cents, provider_fee_cents)
		 SELECT 101, 1, 'paid', 29900, 'RUB', 'yookassa', 'demo-pay-1',  datetime('now','-1 days','+3 hours'), datetime('now','-1 days'), datetime('now','-1 days'), 29900, 500
		 WHERE NOT EXISTS (SELECT 1 FROM orders WHERE provider_payment_id='demo-pay-1')`,
		`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, provider_payment_id, created_at, paid_at, activated_at, callback_amount_cents, provider_fee_cents)
		 SELECT 102, 2, 'paid', 79900, 'RUB', 'yookassa', 'demo-pay-2',  datetime('now','-2 days','+1 hours'), datetime('now','-2 days'), datetime('now','-2 days'), 79900, 900
		 WHERE NOT EXISTS (SELECT 1 FROM orders WHERE provider_payment_id='demo-pay-2')`,
		`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, provider_payment_id, created_at, paid_at, activated_at, callback_amount_cents, provider_fee_cents)
		 SELECT 103, 1, 'paid', 29900, 'RUB', 'yookassa', 'demo-pay-3',  datetime('now','-3 days','+5 hours'), datetime('now','-3 days'), datetime('now','-3 days'), 29900, 500
		 WHERE NOT EXISTS (SELECT 1 FROM orders WHERE provider_payment_id='demo-pay-3')`,
		`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, provider_payment_id, created_at, paid_at, activated_at, callback_amount_cents, provider_fee_cents)
		 SELECT 104, 3, 'paid', 149900, 'RUB', 'yookassa', 'demo-pay-4',  datetime('now','-5 days','+2 hours'), datetime('now','-5 days'), datetime('now','-5 days'), 150000, 1800
		 WHERE NOT EXISTS (SELECT 1 FROM orders WHERE provider_payment_id='demo-pay-4')`,
		`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, provider_payment_id, created_at, paid_at, activated_at, callback_amount_cents, provider_fee_cents)
		 SELECT 105, 1, 'paid', 29900, 'RUB', 'yookassa', 'demo-pay-5',  datetime('now','-8 days','+4 hours'), datetime('now','-8 days'), datetime('now','-8 days'), NULL, 500
		 WHERE NOT EXISTS (SELECT 1 FROM orders WHERE provider_payment_id='demo-pay-5')`,
		`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, provider_payment_id, created_at, paid_at, activated_at, callback_amount_cents, provider_fee_cents)
		 SELECT 106, 4, 'paid', 299900, 'RUB', 'yookassa', 'demo-pay-6',  datetime('now','-12 days','+6 hours'), datetime('now','-12 days'), datetime('now','-12 days'), 299900, 3600
		 WHERE NOT EXISTS (SELECT 1 FROM orders WHERE provider_payment_id='demo-pay-6')`,
		`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, provider_payment_id, created_at, paid_at, activated_at, callback_amount_cents, provider_fee_cents)
		 SELECT 107, 2, 'paid', 79900, 'RUB', 'yookassa', 'demo-pay-7',  datetime('now','-15 days','+2 hours'), datetime('now','-15 days'), datetime('now','-15 days'), 79900, 900
		 WHERE NOT EXISTS (SELECT 1 FROM orders WHERE provider_payment_id='demo-pay-7')`,
		`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, provider_payment_id, created_at, paid_at, activated_at, callback_amount_cents, provider_fee_cents)
		 SELECT 111, 1, 'paid', 29900, 'RUB', 'yookassa', 'demo-pay-8',  datetime('now','-19 days','+3 hours'), datetime('now','-19 days'), datetime('now','-19 days'), 29900, 500
		 WHERE NOT EXISTS (SELECT 1 FROM orders WHERE provider_payment_id='demo-pay-8')`,
		`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, provider_payment_id, created_at, paid_at, activated_at, callback_amount_cents, provider_fee_cents)
		 SELECT 112, 5, 'paid', 499, 'USD', 'stripe', 'demo-pay-9',  datetime('now','-21 days','+8 hours'), datetime('now','-21 days'), datetime('now','-21 days'), 499, 30
		 WHERE NOT EXISTS (SELECT 1 FROM orders WHERE provider_payment_id='demo-pay-9')`,
		`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, provider_payment_id, created_at, paid_at, activated_at, callback_amount_cents, provider_fee_cents)
		 SELECT 113, 2, 'paid', 79900, 'RUB', 'yookassa', 'demo-pay-10', datetime('now','-27 days','+1 hours'), datetime('now','-27 days'), datetime('now','-27 days'), 79900, 900
		 WHERE NOT EXISTS (SELECT 1 FROM orders WHERE provider_payment_id='demo-pay-10')`,
		`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, provider_payment_id, created_at, paid_at, activated_at, callback_amount_cents, provider_fee_cents)
		 SELECT 115, 1, 'paid', 29900, 'RUB', 'yookassa', 'demo-pay-11', datetime('now','-33 days','+4 hours'), datetime('now','-33 days'), datetime('now','-33 days'), 29900, 500
		 WHERE NOT EXISTS (SELECT 1 FROM orders WHERE provider_payment_id='demo-pay-11')`,
		`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, provider_payment_id, created_at, paid_at, activated_at, callback_amount_cents, provider_fee_cents)
		 SELECT 117, 5, 'paid', 499, 'USD', 'stripe', 'demo-pay-12', datetime('now','-40 days','+2 hours'), datetime('now','-40 days'), datetime('now','-40 days'), 505, 30
		 WHERE NOT EXISTS (SELECT 1 FROM orders WHERE provider_payment_id='demo-pay-12')`,
		// Неоплаченные: очередь и история отказов.
		`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, created_at, payment_url, payment_expires_at)
		 SELECT 101, 2, 'pending', 79900, 'RUB', 'yookassa', datetime('now','-5 hours'), 'https://yoomoney.ru/checkout/demo', datetime('now','+55 minutes')
		 WHERE NOT EXISTS (SELECT 1 FROM orders WHERE status='pending')`,
		`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, created_at)
		 SELECT 102, 1, 'expired', 29900, 'RUB', 'yookassa', datetime('now','-4 days')
		 WHERE (SELECT COUNT(*) FROM orders WHERE status='expired') < 2`,
		`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, created_at)
		 SELECT 103, 3, 'expired', 149900, 'RUB', 'yookassa', datetime('now','-9 days')
		 WHERE (SELECT COUNT(*) FROM orders WHERE status='expired') < 2`,
		`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, created_at)
		 SELECT 104, 1, 'canceled', 29900, 'RUB', 'yookassa', datetime('now','-7 days')
		 WHERE (SELECT COUNT(*) FROM orders WHERE status='canceled') < 2`,
	}

	for _, statement := range statements {
		execErr := db.Exec(statement).Error
		if execErr != nil {
			logger.Warn("demo seed statement failed",
				zap.String("statement", strings.SplitN(statement, "\n", 2)[0]),
				zap.Error(execErr))
		}
	}
}

// seedRichDemoBroadcasts — четыре рассылки во всех статусах, у завершённой —
// полный delivery_report для секции «Не доставлено».
func seedRichDemoBroadcasts(db *gorm.DB) {
	statements := []string{
		`INSERT INTO broadcasts (name, filters, message_text, status, recipients_total, sent_count, blocked_count, unreachable_count, failed_count, delivery_report, created_at, updated_at, started_at, finished_at)
		 SELECT 'Оплата прошла', '{}', '*Спасибо за оплату!* Подписка продлена.', 'completed', 10, 7, 2, 1, 2,
		 '{"delivered":[991001,991002,991003,991004,991005,991006,991007],"blocked":[991013,991017],"unreachable":[991014],"errors":[{"telegram_id":991015,"error":"Forbidden: bot was blocked by the user"},{"telegram_id":991016,"error":"Bad Request: chat not found"}],"not_processed":[]}',
		 datetime('now','-3 days'), datetime('now','-3 days'), datetime('now','-3 days'), datetime('now','-3 days')
		 WHERE NOT EXISTS (SELECT 1 FROM broadcasts WHERE name='Оплата прошла')`,
		`INSERT INTO broadcasts (name, filters, message_text, status, recipients_total, planned_at, created_at, updated_at)
		 SELECT 'Анонс новых узлов', '{"plan":"premium"}', 'У нас _новые узлы_ во Франкфурте и Амстердаме!', 'scheduled', 9, datetime('now','+2 hours'), datetime('now','-1 days'), datetime('now','-1 days')
		 WHERE NOT EXISTS (SELECT 1 FROM broadcasts WHERE name='Анонс новых узлов')`,
		`INSERT INTO broadcasts (name, filters, message_text, status, recipients_total, sent_count, failed_count, last_error, retry_count, retry_at, delivery_report, created_at, updated_at, started_at)
		 SELECT 'Акция (упала)', '{}', 'Скидка 20% до конца месяца', 'failed', 5, 2, 3, 'telegram: 429 Too Many Requests: retry after 900', 2, datetime('now','+14 minutes'),
		 '{"delivered":[991001,991002],"blocked":[],"unreachable":[],"errors":[{"telegram_id":991003,"error":"429 Too Many Requests"}],"not_processed":[991004,991005,991006]}',
		 datetime('now','-6 hours'), datetime('now','-6 hours'), datetime('now','-6 hours')
		 WHERE NOT EXISTS (SELECT 1 FROM broadcasts WHERE name='Акция (упала)')`,
		`INSERT INTO broadcasts (name, filters, message_text, status, recipients_total, created_at, updated_at)
		 SELECT 'Черновик праздника', '{}', 'С праздником!', 'canceled', 0, datetime('now','-10 days'), datetime('now','-9 days')
		 WHERE NOT EXISTS (SELECT 1 FROM broadcasts WHERE name='Черновик праздника')`,
	}

	for _, statement := range statements {
		execErr := db.Exec(statement).Error
		if execErr != nil {
			logger.Warn("demo seed statement failed",
				zap.String("statement", strings.SplitN(statement, "\n", 2)[0]),
				zap.Error(execErr))
		}
	}
}

// seedRichDemoMisc — триал-запросы (выраженный топ-IP), инвайты и аудит.
func seedRichDemoMisc(db *gorm.DB) {
	statements := []string{
		`INSERT INTO trial_requests (ip, created_at)
		 SELECT '10.9.9.5', datetime('now','-' || (v.n * 20) || ' minutes')
		 FROM (SELECT 0 n UNION SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5) v
		 WHERE NOT EXISTS (SELECT 1 FROM trial_requests WHERE ip='10.9.9.5')`,
		`INSERT INTO trial_requests (ip, created_at)
		 SELECT '203.0.113.7', datetime('now','-' || (v.n * 3) || ' hours')
		 FROM (SELECT 1 n UNION SELECT 2 UNION SELECT 3) v
		 WHERE NOT EXISTS (SELECT 1 FROM trial_requests WHERE ip='203.0.113.7')`,
		`INSERT INTO trial_requests (ip, created_at)
		 SELECT '198.51.100.9', datetime('now','-5 hours')
		 WHERE NOT EXISTS (SELECT 1 FROM trial_requests WHERE ip='198.51.100.9')`,
		`INSERT INTO trial_requests (ip, created_at)
		 SELECT '192.0.2.44', datetime('now','-20 hours')
		 WHERE NOT EXISTS (SELECT 1 FROM trial_requests WHERE ip='192.0.2.44')`,
		`INSERT INTO trial_requests (ip, created_at)
		 SELECT '198.18.0.23', datetime('now','-23 hours')
		 WHERE NOT EXISTS (SELECT 1 FROM trial_requests WHERE ip='198.18.0.23')`,
		`INSERT OR IGNORE INTO invites (code, referrer_tg_id, created_at)
		 VALUES ('DEMOINV1', 991005, datetime('now','-30 days')),
		        ('DEMOINV2', 991002, datetime('now','-15 days')),
		        ('DEMOINV3', 991011, datetime('now','-2 days'))`,
		`INSERT INTO audit_log (entity_type, entity_id, event, actor_type, actor_id, old_state, new_state, details, created_at)
		 SELECT 'subscription', '101', 'subscription_renewed', 'user', '991001', NULL,
		 '{"plan":"premium","expires_at":"+'||date('now','+2 days')||'"}', '{}', datetime('now','-2 hours')
		 WHERE NOT EXISTS (SELECT 1 FROM audit_log WHERE event='subscription_renewed' AND entity_id='101')`,
		`INSERT INTO audit_log (entity_type, entity_id, event, actor_type, actor_id, details, created_at)
		 SELECT 'order', '1', 'payment_confirmed', 'payment_provider', 'yookassa', '{"amount_cents":29900}', datetime('now','-1 days')
		 WHERE NOT EXISTS (SELECT 1 FROM audit_log WHERE event='payment_confirmed')`,
		`INSERT INTO audit_log (entity_type, entity_id, event, actor_type, actor_id, old_state, new_state, details, created_at)
		 SELECT 'subscription', '117', 'subscription_revoked_by_admin', 'admin', '100', '{"status":"active"}', '{"status":"revoked"}', '{}', datetime('now','-10 days')
		 WHERE NOT EXISTS (SELECT 1 FROM audit_log WHERE event='subscription_revoked_by_admin')`,
		`INSERT INTO audit_log (entity_type, entity_id, event, actor_type, actor_id, details, created_at)
		 SELECT 'broadcast', '1', 'broadcast_completed', 'system', NULL, '{"sent":7,"blocked":2}', datetime('now','-3 days')
		 WHERE NOT EXISTS (SELECT 1 FROM audit_log WHERE event='broadcast_completed')`,
	}

	for _, statement := range statements {
		execErr := db.Exec(statement).Error
		if execErr != nil {
			logger.Warn("demo seed statement failed",
				zap.String("statement", strings.SplitN(statement, "\n", 2)[0]),
				zap.Error(execErr))
		}
	}
}
