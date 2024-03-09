import { useState, useEffect } from 'react';
import outlook from './outlook';
import jira from './jira';

import { ToastContainer, toast } from "react-toastify" 

import {
	TextField,
	CircularProgress,
	Button,
	Autocomplete,
} from '@mui/material';


const Editor = () => {
	const [translating, setTranslating] = useState(false);
	const [validJson, setValidJson] = useState(false);
	const [sourceData, setSourceData] = useState(JSON.stringify(jira, null, 4))
	const [targetData, setTargetData] = useState("");
	const [chosenStandard, setChosenStandard] = useState({});
	const [standards, setStandards] = useState([])


	const outerdiv = {
		display: 'flex',
	}
	const innerdiv = {
		flex: 1,
		padding: 10,
	}

	const loadStandards = () => {
		fetch(`http://localhost:5004/api/v1/standards`, {
			method: 'GET',
			headers: {
				'Content-Type': 'application/json',
			},
			cors: 'cors',
		})
		.then(response => response.json())
		.then(data => {
			if (data.success !== false && data.length > 0) {
				setStandards(data)
				setChosenStandard(data[0])
			}
		}).catch((error) => {
			toast("Failed to get standards: " + error)
		})
	}

	const translate = (inputData, standard) => {
		fetch(`http://localhost:5004/api/v1/translate/to/${standard}`, {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json',
			},
			body: inputData,
			cors: 'cors',
		})
		.then(response => response.json())
		.then(data => {
			console.log('Success:', data);
			setTargetData(JSON.stringify(data, null, 4))
			setTranslating(false)
		}).catch((error) => {
			console.error('Error:', error);
			setTranslating(false)

			toast("Failed to translate: " + error)
		})
	}

	useEffect(() => {
		console.log("In useeffect")
	
		loadStandards() 
	}, [])


	const rows = 31
	return (
		<div>
			<div style={outerdiv}>
				<Button
					style={{
						position: 'absolute',
						top: "45%",
						left: "46%",
						zIndex: 1,
						width: 110,
					}}
					disabled={translating}
					onClick={() => {
						setTranslating(true)
						translate(sourceData, chosenStandard.name)
					}}
					variant="contained"
					color="primary"
				>
					{translating ? <CircularProgress style={{color: "white", }}/> : "Translate"}
				</Button>
				<div style={innerdiv}>
					<div style={{display: "flex", }}>
						<Autocomplete
          				  id="template_action_search"
						  disabled={Object.getOwnPropertyNames(chosenStandard).length === 0} 
          				  autoHighlight
          				  value={chosenStandard}
          				  ListboxProps={{
          				    style: {
          				    },
          				  }}
          				  getOptionLabel={(option) => {
						    var output = option

						    // Check option if it's a dict
							if (option.name !== undefined) {
								output = option.name
							}

							//output = output.replaceAll("_", " ")

          				    return output 
          				  }}
          				  options={standards}
          				  fullWidth
          				  style={{
          				    height: 50,
          				    borderRadius: 5,
          				  }}
          				  onChange={(event, newValue) => {
							console.log("SELECT: ", event, newValue)

							setChosenStandard(newValue)
          				  }}
          				  renderInput={(params) => {
							if (params.inputProps !== undefined && params.inputProps !== null && params.inputProps.value !== undefined && params.inputProps.value !== null) {
								const prefixes = ["Post", "Put", "Patch"]
								for (let [key,keyval] in Object.entries(prefixes)) {
									if (params.inputProps.value.startsWith(prefixes[key])) {
										params.inputProps.value = params.inputProps.value.replace(prefixes[key]+" ", "", -1)
										if (params.inputProps.value.length > 1) {
											params.inputProps.value = params.inputProps.value.charAt(0).toUpperCase()+params.inputProps.value.substring(1)
										}
										break
									}
								}
							}

          				    return (
								<TextField
									style={{
									}}
									{...params}
									label=".. using standard"
									variant="outlined"
          				      	/>
          				    );
          				  }}
          				/>
					</div>
					<TextField
						multiline
						fullWidth
						rows={rows}
						value={sourceData}
						onChange={(e) => {
							const jsoninfo = validateJson(e.target.value)
							console.log("jsoninfo: ", jsoninfo)
							setSourceData(e.target.value)
						}}
					/>
				</div>
				<div style={innerdiv}>
					<TextField
						multiline
						fullWidth
						rows={rows}
						value={targetData}
						onChange={(e) => {
							const jsoninfo = validateJson(e.target.value)
							setTargetData(e.target.value)
						}}
					/>
				</div>
			</div>
			<ToastContainer 
				position="bottom-center"
				autoClose={5000}
				hideProgressBar={false}
				newestOnTop={false}
				closeOnClick
				rtl={false}
				pauseOnFocusLoss
				draggable
				pauseOnHover
				theme="dark"
			/>
		</div>
	)
}

export const validateJson = (showResult) => {
	if (typeof showResult === 'string') {
		showResult = showResult.split(" False").join(" false")
		showResult = showResult.split(" True").join(" true")

		showResult.replaceAll("False,", "false,")
		showResult.replaceAll("True,", "true,")
	}

	if (typeof showResult === "object" || typeof showResult === "array") {
  	  return {
  	    valid: true,
  	    result: showResult,
  	  }
	}

	if (showResult[0] === "\"") {
  		return {
  	  		valid: false,
  	  		result: showResult,
		}
	}

  var jsonvalid = true
  try {
    if (!showResult.includes("{") && !showResult.includes("[")) {
      jsonvalid = false

		return {
			valid: jsonvalid,
			result: showResult,
		};
    }
  } catch (e) {
    showResult = showResult.split("'").join('"');

    try {
      if (!showResult.includes("{") && !showResult.includes("[")) {
        jsonvalid = false;
      }
    } catch (e) {

      jsonvalid = false;
    }
  }

  var result = showResult;
  try {
    result = jsonvalid ? JSON.parse(showResult, {"storeAsString": true}) : showResult;
  } catch (e) {
    ////console.log("Failed parsing JSON even though its valid: ", e)
    jsonvalid = false;
  }

	if (jsonvalid === false) {

		if (typeof showResult === 'string') {
			showResult = showResult.trim()
		}

		try {
			var newstr = showResult.replaceAll("'", '"')

			// Basic workarounds for issues with Python Dicts -> JSON
			if (newstr.includes(": None")) {
				newstr = newstr.replaceAll(": None", ': null')
			}

			if (newstr.includes("[\"{") && newstr.includes("}\"]")) {
				newstr = newstr.replaceAll("[\"{", '[{')
				newstr = newstr.replaceAll("}\"]", '}]')
			}

			if (newstr.includes("{\"[") && newstr.includes("]\"}")) {
				newstr = newstr.replaceAll("{\"[", '[{')
				newstr = newstr.replaceAll("]\"}", '}]')
			}

			result = JSON.parse(newstr)
			jsonvalid = true
		} catch (e) {

			//console.log("Failed parsing JSON even though its valid (2): ", e)
			jsonvalid = false
		}
	}

	if (jsonvalid && typeof result === "number") {
		jsonvalid = false
	}

	// This is where we start recursing
	if (jsonvalid) {
		// Check fields if they can be parsed too 
   	//console.log("In this window for the data. Should look for list in result! Does recursion.") 
		try {
			for (const [key, value] of Object.entries(result)) {
				if (typeof value === "string" && (value.startsWith("{") || value.startsWith("["))) {
					const inside_result = validateJson(value)
					if (inside_result.valid) {
						if (typeof inside_result.result === "string") {
          		const newres = JSON.parse(inside_result.result)
							result[key] = newres 
						} else {
							result[key] = inside_result.result
						}
					}
				} else {

					// Usually only reaches here if raw array > dict > value
					if (typeof showResult !== "array") {
						for (const [subkey, subvalue] of Object.entries(value)) {
							if (typeof subvalue === "string" && (subvalue.startsWith("{") || subvalue.startsWith("["))) {
								const inside_result = validateJson(subvalue)
								if (inside_result.valid) {
									if (typeof inside_result.result === "string") {
										const newres = JSON.parse(inside_result.result)
										result[key][subkey] = newres 
									} else {
										result[key][subkey] = inside_result.result
									}
								}
							}

						}
					}
				}
			}
		} catch (e) {
			console.log("Failed parsing inside json subvalues: ", e)
		}
	}

  return {
    valid: jsonvalid,
    result: result,
  };
}

export default Editor;
