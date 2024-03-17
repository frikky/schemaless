import { useState, useEffect } from 'react';
import outlook from './outlook';
import jira from './jira';

import algoliasearch from 'algoliasearch';
import { InstantSearch, connectSearchBox, connectHits } from 'react-instantsearch-dom';
import { ToastContainer, toast } from "react-toastify" 

import {
	TextField,
	CircularProgress,
	Button,
	Autocomplete,
	InputAdornment,
	Grid,
	Paper,
	Typography,
	Tooltip,

} from '@mui/material';

import {
	Search as SearchIcon,
} from '@mui/icons-material';



const searchClient = algoliasearch("JNSS5CFDZZ", "db08e40265e2941b9a7d8f644b6e5240")
const Editor = () => {
	const [translating, setTranslating] = useState(false);
	const [validJson, setValidJson] = useState(false);
	const [sourceData, setSourceData] = useState(JSON.stringify(jira, null, 4))
	const [targetData, setTargetData] = useState("");
	const [chosenStandard, setChosenStandard] = useState({});
	const [standards, setStandards] = useState([])

	const [open, setOpen] = useState(false);
	const [selectedApp, setSelectedApp] = useState(null);
	const [selectedAppDetails, setSelectedAppDetails] = useState(null);
	const [selectedAction, setSelectedAction] = useState(null);
	const [appData, setAppData] = useState(null);

	const [category, setCategory] = useState("")


	const xs = 12
	const rowHandler = 20
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

	const findExample = (option) => {

		try {
			const baseexample = option.responses.default.content["text/plain"].schema.example
			try {
				return JSON.stringify(JSON.parse(baseexample), null, 4)
			} catch (e) {
				console.log("Failed to parse example: ", e)
				return baseexample
			}
		} catch (e) {
			console.log("Failed to find example: ", e)
			return ""
		}
	}

	const findAction = (appdetails, standard) => {
		// Base64 decode data.app
		const decoded = atob(appdetails.app)
		const parsed = JSON.parse(decoded)

		setSelectedAppDetails(appdetails)

		const defaultResponse = "No example found for this action yet"

		var foundExample = ""
		for (let appactionkey in parsed.actions) {
			const action = parsed.actions[appactionkey]
			if (action.category_label === undefined || action.category_label === null || action.category_label.length === 0) {
				continue
			}

			if (action.name === standard || action.category_label[0] === standard) {
				setSelectedAction(action)
				if (action.returns !== undefined && action.returns !== null && action.returns.example !== undefined && action.returns.example !== null && action.returns.example.length > 0) { 
					foundExample = action.returns.example
				}

				break
			}
		}

		if (foundExample === "") {
			toast("No example found for standard: " + standard)

			// Look into openapi instead?
			if (appdetails.openapi !== undefined && appdetails.openapi !== null) {
				// Base64 decode parsed.openapi
				const decodedOpenapi = atob(appdetails.openapi)
				const parsedOpenApi = JSON.parse(decodedOpenapi)

				const parsedOpenapidata = JSON.parse(parsedOpenApi.body)

				// Loop through paths as second option to find example
				var found = false
				for (let openapikey in parsedOpenapidata.paths) {
					for (let method in parsedOpenapidata.paths[openapikey]) {
						const option = parsedOpenapidata.paths[openapikey][method]
						if (option["x-label"] === standard || option["operationId"] === standard) {
							foundExample = findExample(option)
							if (foundExample === undefined || foundExample === null || foundExample === "") {
								foundExample = defaultResponse
							}

							break
						}
					}

					if (foundExample !== "") {
						break
					}
				}
			}
		}

		if (foundExample !== "") {

			setSourceData(foundExample)
		} else {
			setSourceData("No example data for standard in this app: " + standard)
		}
	}

	const handleAppdataDecoding = (data, standard) => {
		if (data.success === false) {
			toast("Failed to load app data. Please try again")
			return
		}

		// Base64 decode
		if (data.app === undefined || data.app === null) {
			toast("No data.app found in response")

			setAppData(null)
			return
		}

		if (standard === undefined || standard === null) {
			toast("No standard found in response")
			return
		}

		
		findAction(data, standard)
	}

	const loadAppData = (app, standard) => {
		fetch(`https://shuffler.io/api/v1/apps/${app.objectID}/config`, {
			method: 'GET',
			headers: {
				'Content-Type': 'application/json',
			},
			cors: 'cors',
		})
		.then(response => response.json())
		.then(data => {
			console.log('Success:', data);

			handleAppdataDecoding(data, standard)
		})
		.catch((error) => {
			console.error('Error:', error);
			toast("Failed to load app data: " + error)
		})
	}

	const Hits = ({ hits, currentRefinement }) => {
        const [mouseHoverIndex, setMouseHoverIndex] = useState(-1)
        var counted = 0

        return (
            <Grid container spacing={0} style={{ display: "flex", alignItems: "center", justifyContent: "center", width: 300, maxHeight: 400, overflowY: "auto", overflowX: "hidden", position: "absolute", zIndex: 50, border: "1px solid rgba(255,255,255,0.7)", }}>
                {hits.map((data, index) => {
                    const paperStyle = {
                        backgroundColor: index === mouseHoverIndex ? "rgba(255,255,255, 1)" : "#38383A",
                        color: index === mouseHoverIndex ? "red" : "rgba(255,255,255,0.8)",
                        // border: newSelectedApp.objectID !== data.objectID ? `1px solid rgba(255,255,255,0.2)` : "2px solid #f86a3e",
                        textAlign: "left",
                        padding: 10,
                        cursor: "pointer",
                        position: "relative",
                        overflow: "hidden",
                        width: 402,
                        minHeight: 37,
                        maxHeight: 52,
                    }

                    if (counted === 12 / xs * rowHandler) {
                        return null
                    }

                    counted += 1
					var parsedname = data.name.valueOf()
                    parsedname = (parsedname.charAt(0).toUpperCase() + parsedname.substring(1)).replaceAll("_", " ")
                    return (
                        <Paper key={index} elevation={0} style={paperStyle} onMouseOver={() => {
                            setMouseHoverIndex(index)
                        }} onMouseOut={() => {
                            setMouseHoverIndex(-1)
                        }} onClick={() => {
							setSelectedApp(data)

							var standard = ""
							if (data.action_labels !== undefined && data.action_labels !== null && data.action_labels.length > 0) {
								setStandards(data.action_labels)
								setChosenStandard(data.action_labels[0])

								standard = data.action_labels[0]
							} else {
								toast("No action labels found for app: " + data.name)
							}

							if (data.categories !== undefined && data.categories !== null && data.categories.length > 0) {
								setCategory(data.categories[0])
							}

							setOpen(false)

							setSourceData("Loading source data. Please wait a moment.")
							loadAppData(data, standard)
                        }}
						onBlur={() => {
							setOpen(false)
						}}
						>
                            <div style={{ display: "flex" }}>
                                <img alt={data.name} src={data.image_url} style={{ width: "100%", maxWidth: 30, minWidth: 30, minHeight: 30, borderRadius: 40, maxHeight: 30, display: "block", }} />
                                <Typography variant="body1" style={{ marginTop: 2, marginLeft: 10, }}>
                                    {parsedname}
                                </Typography>
                            </div>
                        </Paper>
                    )
                })}
            </Grid>
        )
    }



	const SearchBox = ({ currentRefinement, refine, isSearchStalled }) => {
		return (
	        <div style={{ textAlign: "", display: "flex", }}>
                <form noValidate action="" role="search">
                    <TextField
                        fullWidth
                        variant="outlined"
                        style={{
                            borderRadius: 8,
                            border: 0,
                            boxShadow: 0,
                            margin: 10,
                            height: "",
                            textAlign: "center",
                            // border: "1px solid rgba(241.19, 241.19, 241.19, 0.10)",
                            boxShadow: "none",
                            backgroundColor: "rgba(241.19, 241.19, 241.19, 0.10)",
                            fontWeight: 400,
                            marginLeft: 10,
                            zIndex: 110,
                        }}
			            InputProps={{
                            style: {
                                fontSize: "1em",
                                height: 50,
                                zIndex: 1100,
                                paddingLeft: 15,
                            },
							startAdornment: (
								<InputAdornment position="start">
									{selectedApp === null ? 
										null
										:
										<Tooltip title={`App: ${selectedApp.name}`} placement="top">
											<img alt={selectedApp.name} src={selectedApp.image_url} style={{ width: 30, height: 30, borderRadius: 40, display: "block", }} />
										</Tooltip>
									}
								</InputAdornment>
							),
                            endAdornment: (
                                <InputAdornment position="end">
                                    <div
                                        style={{
                                            width: 42,
                                            height: 36,
                                            background: "#806BFF",
                                            borderRadius: 8,
                                            marginRight: 10,
                                        }}
                                    >
                                        <SearchIcon
                                            onClick={() => {
                                                navigate("/search?q=" + currentRefinement, { state: value, replace: true })
                                                //navigate("/search?q="+currentRefinement, { state: value, replace: true })
                                                //window.open("/apps"+currentRefinement, "_blank")
                                            }}
                                            style={{ cursor: 'pointer', width: "", marginright: 5, marginTop: 7 }}
                                        />
                                    </div>
                                </InputAdornment>
                            ),
                        }}
			            autoComplete="off"
                        type="search"
                        color="primary"
                        placeholder={selectedApp === null ? "Search Apps" : "Using app: " + selectedApp.name.replaceAll("_", " ")}
						value={currentRefinement}
                        id="shuffle_search_field"
                        onChange={(event) => {
                            // Remove "q" from URL
                            // removeQuery("q")
                            refine(event.currentTarget.value)
                        }}
                        onKeyDown={(event) => {
                            if (event.keyCode === 13) {
                                navigate("/search?q=" + currentRefinement, { state: value, replace: true });
                            }
                        }}
                        onClick={(event) => {
                            setOpen(true);
                        }}
                    />
                    {/*isSearchStalled ? 'My search is stalled' : ''*/}
                </form>
            </div>
        )
	}

    const CustomSearchBox = connectSearchBox(SearchBox)
    const CustomHits = connectHits(Hits)

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
						<div style={{flex: 1, }}>
							<InstantSearch searchClient={searchClient} indexName="appsearch">
								<div style={{ maxWidth: 450, margin: "auto", position: "relative", }}>
									<CustomSearchBox />
								</div>
								<div style={{ alignItems: "center", justifyContent: "center", width: "100%", }}>
									{open ? <CustomHits hitsPerPage={1} /> : null}
								</div>
							</InstantSearch>
						</div>

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
						    flex: 1, 
          				    height: 50,
          				    borderRadius: 5,
          				  }}
          				  onChange={(event, newValue) => {
							console.log("SELECT: ", newValue)

							setChosenStandard(newValue)
							setSourceData("Loading source data. Please wait a moment.")
							findAction(selectedAppDetails, newValue)
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
									label={`.. with ${category} standard`}
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
