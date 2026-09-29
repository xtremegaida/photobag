# WD tagger server

A small HTTP server tagging images with one of SmilingWolf's WD v3 ONNX
models, for PhotoBag's Danbooru tags pipeline (Analysis → Pipelines →
Danbooru tags → WD tagger).

## Run it on this computer

    photobag tagger install --download-model

creates a Python environment, installs the requirements and downloads the
model; PhotoBag then starts the tagger when it is needed ("Local
installation"). `photobag tagger serve --host 0.0.0.0 --port 8000` runs
that installation for other computers.

## Host it elsewhere

Put these beside tagger_server.py:

    model.onnx
    selected_tags.csv

from a WD v3 repository on Hugging Face, for example
https://huggingface.co/SmilingWolf/wd-eva02-large-tagger-v3, then:

    python -m venv venv
    venv/bin/pip install -r requirements.txt        (Windows: venv\Scripts\pip)
    venv/bin/python tagger_server.py --host 0.0.0.0 --port 8000

requirements.txt uses onnxruntime-gpu, which needs CUDA and cuDNN. Install
`onnxruntime-gpu[cuda,cudnn]` and set ORT_PRELOAD_DLLS=1 to get them from
pip, or use `onnxruntime` and `--device cpu` without a GPU.

Environment variables: MODEL_PATH, TAGS_PATH, MODEL_NAME, DEVICE (cuda,
cpu or auto), GPU_DEVICE, GENERAL_THRESHOLD, CHARACTER_THRESHOLD,
MAX_UPLOAD_MB, MAX_CONCURRENCY, ORT_PRELOAD_DLLS.

## API

    GET  /health
    POST /tag           multipart "file" → ["tag", ...]
    POST /tag/details   multipart "file" → {"general": [{"tag", "score"}],
                        "characters": [...], "rating": {"tag", "score"}}

Query parameters: general_threshold, character_threshold,
include_characters.
