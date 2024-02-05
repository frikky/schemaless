# Schemaless
A general purpose JSON standardization translation engine, using language models to translate for you

## Goal
Make it easy to standardize the output of given data, no matter what the input was. After the translation has been done with an LLM the first time, the keys are sorted and hashed, and the structure is saved, meaning if it was correct, you will ever only have run the same data structure thorugh the setup ONCE. 

Requires OpenAI API key:
```
export OPENAI_API_KEY=your_key
```

## Use the package
```
go get github.com/frikky/schemaless
```

```
output := schemaless.Translate(ctx context.Context, standard string, userinput string) 
```

## Test it
We built in a test that you can use. Go to the backend folder, and run it:
```
cd backend
go run webservice.go
```

Then in another terminal:
```
sh test.sh
```

## Example:
This is an example that finds matching nested values based on the User Input and puts it in the value of the standard field. The output values should be in a nested jq/shuffle json format.

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
	"kms_key": "secret.name",
	"kms_value": "secret.version.value"
}
```

