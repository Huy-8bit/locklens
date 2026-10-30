package queries

import _ "embed"

//go:embed notifications/delete_notification_channel.sql
var DeleteNotificationChannel string

//go:embed notifications/create_silence.sql
var CreateSilence string

//go:embed notifications/end_silence.sql
var EndSilence string

//go:embed notifications/insert_delivery.sql
var InsertDelivery string

//go:embed notifications/list_deliveries.sql
var ListDeliveries string

//go:embed notifications/list_notification_channels.sql
var ListNotificationChannels string

//go:embed notifications/get_notification_channel.sql
var GetNotificationChannel string

//go:embed notifications/insert_notification_channel.sql
var InsertNotificationChannel string

//go:embed notifications/update_notification_channel.sql
var UpdateNotificationChannel string

//go:embed notifications/open_unacknowledged_incidents.sql
var OpenUnacknowledgedIncidents string

//go:embed notifications/list_silences.sql
var ListSilences string

//go:embed notifications/get_silence.sql
var GetSilence string

//go:embed notifications/active_silence.sql
var ActiveSilence string
