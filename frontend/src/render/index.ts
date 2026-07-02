// The only import surface for components (frontend/src/render/SPEC.md).
export { buildTransform, wx, wy, toWorld, type Transform } from './transform'
export {
  initialCamera, cameraZoom, cameraPan, cameraFollow, cameraTick,
  ZOOM_MIN, ZOOM_MAX, type CameraState, type EntityRef,
} from './camera'
export { drawTerrain, makeTerrainRaster, type TerrainRaster } from './terrain'
export { drawAmbient } from './ambient'
export { drawObjects } from './objects'
export { drawAgents } from './agents'
export { drawFlora } from './flora'
export { drawFauna, faunaSizePx, LUNGE_UNITS } from './fauna'
export { drawFx } from './fx'
export {
  displayPos, displayHeading, isRunning, fxProgress,
  RUN_SPEED_THRESHOLD, type Interpolable,
} from './animator'
export { hitTest } from './hitTest'
