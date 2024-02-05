# Schemaless
A general purpose JSON standardization translation engine

## Goal
Make it easy to standardize the output of given data, no matter what the input was. After the translation has been done with an LLM the first time, the keys are sorted and hashed, and the structure is saved, meaning if it was correct, you will ever only have run the same data structure thorugh the setup ONCE. 

## Example:
This is an example that finds matching nested values based on the User Input and puts it in the value of the standard field.

**Standard**:
```
{
	"kms_key": "The KMS name",
	"kms_value": "The value found for the KMS name"
}
```

**User Input**:
```
{
"secret":
	{
		"name":"username",
		"version":{
			"version":"1",
			"type":"kv",
			"value":"frikky"
		}
	}
}
```

**Expected output**: 
```
{
	"kms_ey": "secret.name",
	"kms_value": "secret.version.value"
}
```

