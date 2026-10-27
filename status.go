package main

import "strings"

// TFS default workflow palette; custom/unknown states stay neutral rather than
// being mistaken for completed work. Labels are kept intact in the templates.
func stateClass(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "список задач":
		return "status-todo"
	case "новый", "новая", "новое", "создано", "создан", "предложено", "new", "proposed", "to do":
		return "status-new"
	case "утверждено", "утвержден", "одобрено", "к выполнению", "approved":
		return "status-ready"
	case "разработка", "в разработке", "выполняется", "в работе", "активный", "активно", "active", "in progress", "committed", "doing":
		return "status-active"
	case "приостановлено", "приостановлен", "приостановлена", "paused", "on hold", "тестирование", "проверка", "на тестировании", "на проверке", "testing", "review", "in review":
		return "status-review"
	case "решено", "решена", "решен", "решён", "разрешено", "resolved":
		return "status-resolved"
	case "выполнено", "выполнена", "выполнен", "закрыто", "закрыта", "закрыт", "готово", "done", "closed", "completed":
		return "status-done"
	case "удалено", "удалена", "удален", "удалён", "отменено", "removed", "cancelled", "canceled":
		return "status-removed"
	case "заблокировано", "заблокирован", "блокировано", "blocked":
		return "status-blocked"
	default:
		return "status-unknown"
	}
}

// PR completion is purple in Azure DevOps, unlike completed work items (green).
func prStatusClass(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active":
		return "status-active"
	case "completed":
		return "status-review"
	default:
		return "status-removed"
	}
}
