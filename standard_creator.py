import os
import json
import requests

def download_event(eventurl, filename):
    eventurl = "https://schema.ocsf.io/api/1.0.0/classes/base_event?profiles="

    request = requests.get(eventurl, verify=False)
    print(request.text)
    print(request.status_code)

    with open(filename, 'w') as f:
        f.write(request.text)

def fix_event(filename): 
    basedata = ""
    with open(filename, "r") as tmp:
        basedata = tmp.read()

    newdata = {}
    jsondata = json.loads(basedata)
    for item in jsondata["attributes"]:
        foundkey = ""
        foundtype = ""
        for key, value in item.items():
            foundkey = key

            if item[foundkey]["type"] == "string_t":
                newdata[foundkey] = ""
            elif item[foundkey]["type"] == "integer_t":
                newdata[foundkey] = 0
            elif item[foundkey]["type"] == "timestamp_t":
                newdata[foundkey] = "2016-01-01T00:00:00.000Z"
            elif item[foundkey]["type"] == "object_t":
                newdata[foundkey] = {}
            else:
                print(item)

            break
            
    print(json.dumps(newdata, indent=4, sort_keys=True))
    new_filename = filename.split("/")[1]
    with open("standards/" + new_filename, "w") as tmp:
        tmp.write(json.dumps(newdata, indent=4, sort_keys=True))

def setup():
    if not os.path.exists("standards"):
        os.makedirs("standards")

    if not os.path.exists("base_standards"):
        os.makedirs("base_standards")

if __name__ == '__main__':
    setup()

    #download_event()
    filename = "base_standards/base_event.json"
    fix_event(filename)
