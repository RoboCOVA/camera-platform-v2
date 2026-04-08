# Ethiopia ANPR Training Recipe (YOLO Plate Detector)

This guide outlines a practical training workflow for an Ethiopian license plate detector that can be used by the edge ANPR sidecar.

## 1. Data Collection

Collect plate images across:

- Day/night
- Different angles (front, rear, oblique)
- Different regions/plate styles
- Motion blur + rain + dust

Target: **5,000–20,000 labeled plates** for strong results.

## 2. Labeling

Label bounding boxes around plates only.

Tools:

- Label Studio
- CVAT
- Roboflow

Export format: **YOLOv5/YOLOv8**

## 3. Model Choice

Start with a lightweight model:

- YOLOv8n or YOLOv5s
- Input: 640×640

## 4. Training

Example (YOLOv8):

```bash
yolo detect train data=plates.yaml model=yolov8n.pt imgsz=640 epochs=100 batch=16
```

## 5. Export to ONNX

```bash
yolo export model=runs/detect/train/weights/best.pt format=onnx
```

Copy the ONNX model to the edge node and set:

```
ANPR_YOLO_MODEL=/opt/cam/anpr/plate_detector.onnx
```

## 6. Validate on Edge

Run sidecar and confirm logs:

- Plate crops produced
- OCR accuracy increases

## 7. Continuous Improvement

Collect false positives/negatives and re‑train quarterly.
