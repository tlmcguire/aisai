
from fastapi import APIRouter, Request
from fastapi import HTTPException
from pydantic import BaseModel, Field
from lollms_webui import LOLLMSWebUI
from pydantic import BaseModel
from starlette.responses import StreamingResponse
from lollms.types import MSG_TYPE
from lollms.main_config import BaseConfig
from lollms.utilities import detect_antiprompt, remove_text_from_string, trace_exception, find_first_available_file_index
from ascii_colors import ASCIIColors
from lollms.databases.discussions_database import DiscussionsDB
from lollms.types import SENDER_TYPES
from typing import List
from pathlib import Path
from safe_store.text_vectorizer import TextVectorizer, VectorizationMethod, VisualizationMethod
import tqdm
from fastapi import FastAPI, UploadFile, File
import shutil
import os
import platform
from urllib.parse import urlparse
from functools import partial
from datetime import datetime
from utilities.execution_engines.python_execution_engine import execute_python
from utilities.execution_engines.latex_execution_engine import execute_latex
from utilities.execution_engines.shell_execution_engine import execute_bash
from lollms.security import sanitize_path, forbid_remote_access
from lollms.internet import scrape_and_save
from urllib.parse import urlparse
import threading

router = APIRouter()
lollmsElfServer:LOLLMSWebUI = LOLLMSWebUI.get_instance()

class AddWebPageRequest(BaseModel):
    client_id: str  = Field(...)
    url: str = Field(..., description="Url to be used")

class CmdExecutionRequest(BaseModel):
    client_id: str  = Field(...)
    command: str = Field(..., description="Url to be used")
    parameters: List[str] = Field(..., description="Command parameters")

MAX_PAGE_SIZE = 10000000

@router.post("/add_webpage")
async def add_webpage(request: AddWebPageRequest):
    forbid_remote_access(lollmsElfServer)
    client = lollmsElfServer.session.get_client(request.client_id)
    if client is None:
        raise HTTPException(status_code=400, detail="Unknown client. This service only accepts lollms webui requests")

    def do_scraping():
        lollmsElfServer.ShowBlockingMessage("Scraping web page\nPlease wait...")
        ASCIIColors.yellow("Scaping web page")
        client = lollmsElfServer.session.get_client(request.client_id)
        url = request.url
        index =  find_first_available_file_index(lollmsElfServer.lollms_paths.personal_uploads_path,"web_",".txt")
        file_path=sanitize_path(lollmsElfServer.lollms_paths.personal_uploads_path/f"web_{index}.txt",True)
        try:
            result = urlparse(url)
            if all([result.scheme, result.netloc]):
                if scrape_and_save(url=url, file_path=file_path,max_size=MAX_PAGE_SIZE):
                    raise HTTPException(status_code=400, detail="Web page too large")
            else:
                raise HTTPException(status_code=400, detail="Invalid URL")
        except Exception as e:
            raise HTTPException(status_code=400, detail=f"Exception : {e}")

        try:
            if not lollmsElfServer.personality.processor is None:
                lollmsElfServer.personality.processor.add_file(file_path, client, partial(lollmsElfServer.process_chunk, client_id = request.client_id))
            else:
                lollmsElfServer.personality.add_file(file_path, client, partial(lollmsElfServer.process_chunk, client_id = request.client_id))
            lollmsElfServer.HideBlockingMessage()
            lollmsElfServer.refresh_files()
        except Exception as e:
            lollmsElfServer.HideBlockingMessage()
            lollmsElfServer.refresh_files()
            return {'status':False,"error":str(e)}
    client.generation_thread = threading.Thread(target=do_scraping)
    client.generation_thread.start()

    return {'status':True}
