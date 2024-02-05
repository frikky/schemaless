#curl localhost:5003/api/v1/translate_to/email -X POST -H "Content-Type: application/json" -d '{"title": "Here is a message for you", "description": "What is this?", "severity": "High", "status": "Open", "time_taken": "125", "id": "1234"}'

#curl localhost:5004/api/v1/translate_from/ticket -X POST -H "Content-Type: application/json" -d '{"item": "I want to buy a ticket"}'
#
#curl localhost:5004/api/v1/translate_to/get_kms_key -X POST -H "Content-Type: application/json" -d '{
#		"name":"username",
#		"version":{
#			"version":"1",
#			"type":"kv",
#			"value":"frikky@shuffler.io"
#		}
#}'

curl localhost:5004/api/v1/translate_to/get_kms_key -X POST -H "Content-Type: application/json" -d '{
"secret":
	{
		"name":"username",
		"version":{
			"version":"1",
			"type":"kv",
			"value":"frikky@shuffler.io"
		}
	}
}'
