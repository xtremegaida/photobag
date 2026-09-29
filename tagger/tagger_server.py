#!/usr/bin/env python3

import argparse
import asyncio
import csv
import io
import os
from pathlib import Path
from typing import Optional

import numpy as np
import onnxruntime as ort
import uvicorn

from fastapi import FastAPI, File, HTTPException, Query, UploadFile
from PIL import Image, ImageOps, UnidentifiedImageError
from starlette.concurrency import run_in_threadpool


# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

MODEL_PATH = os.environ.get("MODEL_PATH", "model.onnx")
TAGS_PATH = os.environ.get("TAGS_PATH", "selected_tags.csv")

GPU_DEVICE = int(os.environ.get("GPU_DEVICE", "0"))

DEFAULT_GENERAL_THRESHOLD = float(
    os.environ.get("GENERAL_THRESHOLD", "0.35")
)

DEFAULT_CHARACTER_THRESHOLD = float(
    os.environ.get("CHARACTER_THRESHOLD", "0.85")
)

MAX_UPLOAD_MB = int(os.environ.get("MAX_UPLOAD_MB", "32"))

# Limiting concurrency is generally sensible for one large GPU model.
MAX_CONCURRENCY = int(os.environ.get("MAX_CONCURRENCY", "1"))


# HuggingFace selected_tags.csv categories:
CATEGORY_GENERAL = 0
CATEGORY_CHARACTER = 4
CATEGORY_RATING = 9


# ---------------------------------------------------------------------------
# Tagger
# ---------------------------------------------------------------------------

class WDTagger:
    def __init__(
        self,
        model_path: str,
        tags_path: str,
        device_id: int = 0,
    ):
        self.model_path = Path(model_path)
        self.tags_path = Path(tags_path)
        self.device_id = device_id

        if not self.model_path.exists():
            raise RuntimeError(
                f"ONNX model not found: {self.model_path}"
            )

        if not self.tags_path.exists():
            raise RuntimeError(
                f"Tag CSV not found: {self.tags_path}"
            )

        self.names: list[str] = []
        self.categories: list[int] = []

        self._load_tags()
        self._load_model()

    def _load_tags(self):
        with self.tags_path.open(
            "r",
            encoding="utf-8-sig",
            newline="",
        ) as f:
            reader = csv.DictReader(f)

            required = {"name", "category"}

            if reader.fieldnames is None:
                raise RuntimeError("selected_tags.csv has no header")

            missing = required - set(reader.fieldnames)

            if missing:
                raise RuntimeError(
                    "selected_tags.csv is missing required columns: "
                    + ", ".join(sorted(missing))
                )

            for row in reader:
                self.names.append(row["name"])
                self.categories.append(int(row["category"]))

        if not self.names:
            raise RuntimeError(
                "No tags were loaded from selected_tags.csv"
            )

        print(f"Loaded {len(self.names):,} tags")

    def _load_model(self):
        available = ort.get_available_providers()

        print("Available ONNX Runtime providers:")
        for provider in available:
            print(f"  {provider}")

        if "CUDAExecutionProvider" not in available:
            raise RuntimeError(
                "CUDAExecutionProvider is not available.\n"
                "Make sure you installed onnxruntime-gpu rather than "
                "onnxruntime, and that the required CUDA/cuDNN libraries "
                "are available."
            )

        providers = [
            (
                "CUDAExecutionProvider",
                {
                    "device_id": self.device_id,
                    "cudnn_conv_algo_search": "EXHAUSTIVE",
                    "do_copy_in_default_stream": "1",
                },
            ),

            # Allows individual unsupported operators to fall back to CPU.
            # CUDA is still the preferred execution provider.
            "CPUExecutionProvider",
        ]

        options = ort.SessionOptions()

        self.session = ort.InferenceSession(
            str(self.model_path),
            sess_options=options,
            providers=providers,
        )

        active = self.session.get_providers()

        print("Active ONNX Runtime providers:")
        for provider in active:
            print(f"  {provider}")

        if "CUDAExecutionProvider" not in active:
            raise RuntimeError(
                "The ONNX model loaded, but CUDAExecutionProvider "
                "is not active."
            )

        inputs = self.session.get_inputs()
        outputs = self.session.get_outputs()

        if len(inputs) != 1:
            raise RuntimeError(
                f"Expected 1 input tensor, found {len(inputs)}"
            )

        if len(outputs) < 1:
            raise RuntimeError("Model has no output tensors")

        input_info = inputs[0]

        self.input_name = input_info.name
        self.output_name = outputs[0].name
        self.input_shape = input_info.shape
        self.output_shape = outputs[0].shape

        print(f"Input:  {self.input_name} {self.input_shape}")
        print(f"Output: {self.output_name} {self.output_shape}")

        # WD v3 ONNX models use:
        #
        #   [batch, height, width, channels]
        #
        # EVA02 Large is normally:
        #
        #   [None, 448, 448, 3]

        if len(self.input_shape) != 4:
            raise RuntimeError(
                f"Unexpected input shape: {self.input_shape}"
            )

        height = self.input_shape[1]
        width = self.input_shape[2]
        channels = self.input_shape[3]

        if not isinstance(height, int) or not isinstance(width, int):
            raise RuntimeError(
                "ONNX model has dynamic image dimensions. "
                "Expected fixed H/W dimensions."
            )

        if channels != 3:
            raise RuntimeError(
                f"Expected 3 input channels, got {channels}"
            )

        if height != width:
            raise RuntimeError(
                f"Expected square input, got {width}x{height}"
            )

        self.target_size = height

        # Check output count when it is available statically.
        if self.output_shape:
            output_count = self.output_shape[-1]

            if (
                isinstance(output_count, int)
                and output_count != len(self.names)
            ):
                raise RuntimeError(
                    f"Model has {output_count} outputs but "
                    f"selected_tags.csv contains {len(self.names)} tags. "
                    "Make sure the ONNX and CSV files came from the "
                    "same model revision."
                )

        print(
            f"WD tagger ready: {self.target_size}x{self.target_size}, "
            f"GPU {self.device_id}"
        )

    def prepare_image(self, image: Image.Image) -> np.ndarray:
        """
        Prepare an image using the preprocessing expected by the
        SmilingWolf WD ONNX models.

        Output shape:
            [1, H, W, 3]

        Pixel format:
            float32 BGR, range 0..255
        """

        # Correct EXIF rotation before processing.
        image = ImageOps.exif_transpose(image)

        # Handle transparency against a white background.
        image = image.convert("RGBA")

        canvas = Image.new(
            "RGBA",
            image.size,
            (255, 255, 255, 255),
        )

        canvas.alpha_composite(image)
        image = canvas.convert("RGB")

        width, height = image.size
        max_dim = max(width, height)

        # Pad to square.
        pad_left = (max_dim - width) // 2
        pad_top = (max_dim - height) // 2

        padded = Image.new(
            "RGB",
            (max_dim, max_dim),
            (255, 255, 255),
        )

        padded.paste(
            image,
            (pad_left, pad_top),
        )

        # Resize to model dimensions.
        if max_dim != self.target_size:
            padded = padded.resize(
                (self.target_size, self.target_size),
                Image.Resampling.BICUBIC,
            )

        array = np.asarray(
            padded,
            dtype=np.float32,
        )

        # PIL gives us RGB.
        # WD ONNX models expect BGR.
        array = array[:, :, ::-1]

        # Ensure memory is contiguous because ::-1 creates a negative stride.
        array = np.ascontiguousarray(array)

        # NHWC batch.
        array = np.expand_dims(array, axis=0)

        return array

    def predict(
        self,
        image: Image.Image,
        general_threshold: float,
        character_threshold: float,
        include_characters: bool,
    ) -> list[str]:

        input_tensor = self.prepare_image(image)

        predictions = self.session.run(
            [self.output_name],
            {
                self.input_name: input_tensor,
            },
        )[0]

        scores = predictions[0]

        if len(scores) != len(self.names):
            raise RuntimeError(
                f"Model returned {len(scores)} values but "
                f"{len(self.names)} tag names are loaded."
            )

        results: list[tuple[str, float]] = []

        for name, category, score in zip(
            self.names,
            self.categories,
            scores,
        ):
            score = float(score)

            if category == CATEGORY_GENERAL:
                if score > general_threshold:
                    results.append((name, score))

            elif (
                category == CATEGORY_CHARACTER
                and include_characters
            ):
                if score > character_threshold:
                    results.append((name, score))

            # Ratings are deliberately not included in /tag.

        # Most confident tags first.
        results.sort(
            key=lambda item: item[1],
            reverse=True,
        )

        # Return an actual JSON array of tag names.
        return [name for name, _ in results]

    def predict_detailed(
        self,
        image: Image.Image,
        general_threshold: float,
        character_threshold: float,
        include_characters: bool,
    ) -> dict:

        input_tensor = self.prepare_image(image)

        predictions = self.session.run(
            [self.output_name],
            {
                self.input_name: input_tensor,
            },
        )[0][0]

        general = []
        characters = []
        ratings = []

        for name, category, score in zip(
            self.names,
            self.categories,
            predictions,
        ):
            score = float(score)

            item = {
                "tag": name,
                "score": score,
            }

            if category == CATEGORY_GENERAL:
                if score > general_threshold:
                    general.append(item)

            elif category == CATEGORY_CHARACTER:
                if include_characters and score > character_threshold:
                    characters.append(item)

            elif category == CATEGORY_RATING:
                ratings.append(item)

        general.sort(
            key=lambda x: x["score"],
            reverse=True,
        )

        characters.sort(
            key=lambda x: x["score"],
            reverse=True,
        )

        ratings.sort(
            key=lambda x: x["score"],
            reverse=True,
        )

        return {
            "general": general,
            "characters": characters,
            "rating": ratings[0] if ratings else None,
        }


# ---------------------------------------------------------------------------
# Application state
# ---------------------------------------------------------------------------

tagger: Optional[WDTagger] = None
inference_semaphore: Optional[asyncio.Semaphore] = None


# ---------------------------------------------------------------------------
# FastAPI
# ---------------------------------------------------------------------------

app = FastAPI(
    title="WD EVA02 Large Tagger v3",
    description=(
        "GPU accelerated image tagging using "
        "wd-eva02-large-tagger-v3 and ONNX Runtime."
    ),
    version="1.0",
)


@app.on_event("startup")
async def startup():
    global tagger
    global inference_semaphore

    tagger = WDTagger(
        model_path=MODEL_PATH,
        tags_path=TAGS_PATH,
        device_id=GPU_DEVICE,
    )

    inference_semaphore = asyncio.Semaphore(
        MAX_CONCURRENCY
    )


async def read_image(file: UploadFile) -> Image.Image:
    limit = MAX_UPLOAD_MB * 1024 * 1024

    data = await file.read(limit + 1)

    if len(data) > limit:
        raise HTTPException(
            status_code=413,
            detail=(
                f"Image exceeds maximum upload size "
                f"of {MAX_UPLOAD_MB} MB"
            ),
        )

    if not data:
        raise HTTPException(
            status_code=400,
            detail="Empty upload",
        )

    try:
        image = Image.open(io.BytesIO(data))

        # Force decoding now, while the backing BytesIO exists.
        image.load()

        return image

    except UnidentifiedImageError:
        raise HTTPException(
            status_code=400,
            detail="Uploaded file is not a supported image",
        )

    except Exception as exc:
        raise HTTPException(
            status_code=400,
            detail=f"Unable to decode image: {exc}",
        )


@app.get("/health")
async def health():
    if tagger is None:
        raise HTTPException(
            status_code=503,
            detail="Model not loaded",
        )

    return {
        "status": "ok",
        "model": str(tagger.model_path),
        "device": GPU_DEVICE,
        "providers": tagger.session.get_providers(),
        "input_shape": tagger.input_shape,
        "output_shape": tagger.output_shape,
        "tag_count": len(tagger.names),
    }


@app.post("/tag", response_model=list[str])
async def tag(
    file: UploadFile = File(...),

    general_threshold: float = Query(
        DEFAULT_GENERAL_THRESHOLD,
        ge=0.0,
        le=1.0,
    ),

    character_threshold: float = Query(
        DEFAULT_CHARACTER_THRESHOLD,
        ge=0.0,
        le=1.0,
    ),

    include_characters: bool = Query(True),
):
    """
    Tag an uploaded image.

    Returns a plain JSON array:

        [
            "1girl",
            "solo",
            "long_hair",
            ...
        ]
    """

    if tagger is None or inference_semaphore is None:
        raise HTTPException(
            status_code=503,
            detail="Model not loaded",
        )

    image = await read_image(file)

    try:
        async with inference_semaphore:
            return await run_in_threadpool(
                tagger.predict,
                image,
                general_threshold,
                character_threshold,
                include_characters,
            )

    except Exception as exc:
        raise HTTPException(
            status_code=500,
            detail=f"Inference failed: {exc}",
        )


@app.post("/tag/details")
async def tag_details(
    file: UploadFile = File(...),

    general_threshold: float = Query(
        DEFAULT_GENERAL_THRESHOLD,
        ge=0.0,
        le=1.0,
    ),

    character_threshold: float = Query(
        DEFAULT_CHARACTER_THRESHOLD,
        ge=0.0,
        le=1.0,
    ),

    include_characters: bool = Query(True),
):
    """
    Optional endpoint returning probabilities and the predicted rating.
    """

    if tagger is None or inference_semaphore is None:
        raise HTTPException(
            status_code=503,
            detail="Model not loaded",
        )

    image = await read_image(file)

    try:
        async with inference_semaphore:
            return await run_in_threadpool(
                tagger.predict_detailed,
                image,
                general_threshold,
                character_threshold,
                include_characters,
            )

    except Exception as exc:
        raise HTTPException(
            status_code=500,
            detail=f"Inference failed: {exc}",
        )


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(
        description="WD EVA02 Large Tagger v3 HTTP server"
    )

    parser.add_argument(
        "--host",
        default="0.0.0.0",
    )

    parser.add_argument(
        "--port",
        type=int,
        default=8000,
    )

    args = parser.parse_args()

    # Important: use one worker unless you explicitly want multiple copies
    # of the ~1.26 GB ONNX model loaded into GPU memory.
    uvicorn.run(
        app,
        host=args.host,
        port=args.port,
        workers=1,
    )


if __name__ == "__main__":
    main()