package bot

import "fmt"

type MessageKey string

const (
	MsgStartGreeting      MessageKey = "start.greeting"
	MsgStartGreetingNoSub MessageKey = "start.greeting_no_sub"
	MsgStartButton        MessageKey = "start.button"
	MsgLoading            MessageKey = "loading"
	MsgUnknownCommand     MessageKey = "unknown_command"
	MsgUseStart           MessageKey = "use_start"

	MsgSubNoActive       MessageKey = "sub.no_active"
	MsgSubTempError      MessageKey = "sub.temp_error"
	MsgSubCreateError    MessageKey = "sub.create_error"
	MsgSubCreatedSuccess MessageKey = "sub.created_success"

	MsgAdminNoSubs       MessageKey = "admin.no_subs"
	MsgAdminLastReg      MessageKey = "admin.last_reg"
	MsgAdminDelUsage     MessageKey = "admin.del_usage"
	MsgAdminDelInvalidID MessageKey = "admin.del_invalid_id"
	MsgAdminDelNotFound  MessageKey = "admin.del_not_found"
	MsgAdminDelXUIFailed MessageKey = "admin.del_xui_failed"
	MsgAdminDelPartial   MessageKey = "admin.del_partial"
	MsgAdminDelSuccess   MessageKey = "admin.del_success"

	MsgAdminBroadcastUsage   MessageKey = "admin.broadcast_usage"
	MsgAdminBroadcastLong    MessageKey = "admin.broadcast_long"
	MsgAdminBroadcastNoUsers MessageKey = "admin.broadcast_no_users"
	MsgAdminBroadcastStart   MessageKey = "admin.broadcast_start"
	MsgAdminBroadcastPartial MessageKey = "admin.broadcast_partial"
	MsgAdminBroadcastDone    MessageKey = "admin.broadcast_done"

	MsgAdminSendUsage       MessageKey = "admin.send_usage"
	MsgAdminSendNotFound    MessageKey = "admin.send_not_found"
	MsgAdminSendFailed      MessageKey = "admin.send_failed"
	MsgAdminSendSuccess     MessageKey = "admin.send_success"
	MsgAdminSendRateLimited MessageKey = "admin.send_rate_limited"

	MsgAdminRefStats MessageKey = "admin.ref_stats"
	MsgAdminStats    MessageKey = "admin.stats"

	MsgInviteCreateFailed MessageKey = "invite.create_failed"

	MsgQRCodeFailed  MessageKey = "qr.failed"
	MsgQRCodeCaption MessageKey = "qr.caption"
	MsgQRCodeBack    MessageKey = "qr.back"

	MsgErrConnection      MessageKey = "err.connection"
	MsgErrAuth            MessageKey = "err.auth"
	MsgErrRequestCanceled MessageKey = "err.request_canceled"
	MsgErrDialTCP         MessageKey = "err.dial_tcp"
	MsgErrTLS             MessageKey = "err.tls"
	MsgErrInboundNotFound MessageKey = "err.inbound_not_found"
	MsgErrClientExists    MessageKey = "err.client_exists"
	MsgErrGeneric         MessageKey = "err.generic"
	MsgErrPartialSave     MessageKey = "err.partial_save"
)

var messages = map[MessageKey]string{
	MsgStartGreeting:      "👋 Привет, %s!\n\nЯ бот для выдачи подписок на прокси VLESS+Reality+Vision.\n\nИспользуйте кнопки ниже для взаимодействия с ботом.",
	MsgStartGreetingNoSub: "👋 Привет, %s!\n\nЯ бот для выдачи подписок на прокси VLESS+Reality+Vision.\n\nНажмите кнопку ниже, чтобы получить подписку",
	MsgStartButton:        "📥 Получить подписку",
	MsgLoading:            "⏳ Загрузка...",
	MsgUnknownCommand:     "Неизвестная команда. Используйте /start или /help",
	MsgUseStart:           "Используйте /start д��я начала работы с ботом.",

	MsgSubNoActive:       "❌ У вас нет активной подписки.\n\nНажмите «Получить подписку» для создания.",
	MsgSubTempError:      "❌ Временная ошибка. Попробуйте позже.",
	MsgSubCreateError:    "❌ Ошибка при создании подписки.",
	MsgSubCreatedSuccess: "✅ Ваша подписка\n\n📊 Трафик: %d ГБ\n\n🔗 Ссылка\n`%s`",

	MsgAdminNoSubs:       "📭 Нет активных подписок",
	MsgAdminLastReg:      "📋 *Последние регистрации*\n\n",
	MsgAdminDelUsage:     "❌ Использование: /del <id>\n\nПример: /del 5",
	MsgAdminDelInvalidID: "❌ ID должен быть положительным числом",
	MsgAdminDelNotFound:  "❌ Подписка с ID %d не найдена",
	MsgAdminDelXUIFailed: "❌ Ошибка удаления клиента из панели 3x-ui",
	MsgAdminDelPartial:   "⚠️ Клиент удален из панели, но ошибка удаления из базы",
	MsgAdminDelSuccess:   "✅ Подписка успешно удалена!\n\n👤 Пользователь: @%s\n🆔 Telegram ID: %d",

	MsgAdminBroadcastUsage:   "❌ Использование: /broadcast <сообщение>\n\nПример: /broadcast Привет всем!",
	MsgAdminBroadcastLong:    "❌ Сообщение слишком длинное (%d символов).\n\nМаксимум: %d символов.",
	MsgAdminBroadcastNoUsers: "❌ Нет пользователей для рассылки",
	MsgAdminBroadcastStart:   "📤 Начинаю рассылку для %d пользователей...",
	MsgAdminBroadcastPartial: "⚠️ Рассылка прервана!\n\n📤 Отправлено: %d\n❌ Ошибок: %d\n👥 Осталось: %d",
	MsgAdminBroadcastDone:    "✅ Рассылка завершена!\n\n📤 Отправлено: %d\n❌ Ошибок: %d\n👥 Всего пользователей: %d",

	MsgAdminSendUsage:       "❌ Использование: /send <telegram_id|username> <сообщение>\n\nПримеры:\n/send 123456789 Привет!\n/send @username Привет!",
	MsgAdminSendNotFound:    "❌ Пользователь @%s не найден в базе",
	MsgAdminSendFailed:      "❌ Ошибка отправки сообщения: %v",
	MsgAdminSendSuccess:     "✅ Сообщение отправлено!\n\n👤 Получатель: %d\n💬 ID сообщения: %d",
	MsgAdminSendRateLimited: "⚠️ Слишком много сообщений. Подождите минуту.",

	MsgAdminRefStats: "📊 *Статистика рефералов*\n\n",
	MsgAdminStats:    "📊 *Статистика бота*\n\n👥 Всего подписок: %d\n✅ Активных: %d\n🎁 Триалов: %d\n🆕 За сегодня: %d",

	MsgInviteCreateFailed: "❌ Не удалось создать пригласительную ссылку. Попробуйте позже.",

	MsgQRCodeFailed:  "❌ Ошибка генерации QR-кода. Попробуйте позже.",
	MsgQRCodeCaption: "📱 QR-код с подпиской\n\nНаведите камеру телефона на код, чтобы импортировать подписку",
	MsgQRCodeBack:    "Назад",

	MsgErrConnection:      "❌ Не удается подключиться к серверу. Попробу��те позже.",
	MsgErrAuth:            "❌ Ошибка авторизации на сервере. Свяжитесь с администратором.",
	MsgErrRequestCanceled: "❌ Запрос был прерван. Попробуйте снова.",
	MsgErrDialTCP:         "❌ Ошибка подключения к серверу. Проверьте настройки DNS.",
	MsgErrTLS:             "❌ Ошибка SSL/TLS сертификата. Свяжитесь с администратором.",
	MsgErrInboundNotFound: "❌ Ошибка сервера при создании подписки",
	MsgErrClientExists:    "❌ Ошибка сервера при создании подписки",
	MsgErrGeneric:         "❌ Ошибка при создании подписки.",
	MsgErrPartialSave:     "❌ Подписка создана в панели, но не сохранена в базе. Обратитесь к администратору.",
}

func msg(key MessageKey, a ...interface{}) string {
	return fmt.Sprintf(messages[key], a...)
}

func getMsg(key MessageKey) string {
	return messages[key]
}
