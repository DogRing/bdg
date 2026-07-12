// GLSL for the curved-world hex renderer (WebGL1 + ANGLE_instanced_arrays).
// - tile: instanced hex prisms. Vertex shader bends the world down with view-space depth
//   (rolling/curved world), extrudes side walls only toward LOWER neighbours (no z-fight),
//   ripples water tops (travel direction/speed = wind), and shades with one directional
//   light whose ambient/diffuse intensities are uniforms (day-night ramp). Fragment tints
//   by the light colour, darkens under drifting cloud-shadow noise, and fogs into the sky.
// - sky: ray-based (camera basis uniforms): horizon-locked gradient + procedural sun/moon
//   discs + hash stars at night. Horizon colour == the fog colour → seamless join.
// Atmosphere uniform values come from gl/atmosphere.ts (docs/plans/gl-atmosphere.md).

export const TILE_VS = `
precision highp float;
attribute vec2 aLocal;      // unit hex-corner xz (centre = 0,0)
attribute float aTop;       // 1 = top ring, 0 = bottom ring of a wall
attribute float aFace;      // 0 = top face, 1 = side wall
attribute vec3 aNormal;
attribute float aEdge;      // side wall: which of the 6 edges (0..5)
attribute vec2 iCenter;     // per-instance hex centre (world x,z)
attribute float iType;      // per-instance tile type 0..4 (3 = water)
attribute float iElev;      // per-instance top elevation (world units)
attribute vec3 iNbrA;       // neighbour top elevations for edges 0,1,2
attribute vec3 iNbrB;       // neighbour top elevations for edges 3,4,5
uniform mat4 uProj, uView;
uniform float uCurv, uRelief, uCols, uTime, uHexR, uRipple;
uniform float uAmbI, uDiffI;
uniform vec2 uCell, uWindDir;
uniform float uWindSpd;
uniform vec3 uLight;
varying vec2 vUV; varying float vDepth; varying float vLight; varying float vWater;
varying vec2 vWXZ;
float nbr(float e){
  if(e<0.5) return iNbrA.x; else if(e<1.5) return iNbrA.y; else if(e<2.5) return iNbrA.z;
  else if(e<3.5) return iNbrB.x; else if(e<4.5) return iNbrB.y; return iNbrB.z;
}
void main(){
  vec2 hxz = iCenter + aLocal*uHexR;
  float bottomY = min(iElev, nbr(aEdge));                 // wall drops only to the lower neighbour
  float restRaw = (aFace<0.5 || aTop>0.5) ? iElev : bottomY;
  float atTop   = (aFace<0.5 || aTop>0.5) ? 1.0 : 0.0;
  float water   = (abs(iType-3.0)<0.5) ? 1.0 : 0.0;
  float y = restRaw*uRelief
          + water*atTop*uRelief*uRipple*sin(uTime*uWindSpd + dot(hxz, uWindDir)/uHexR);
  vec4 vp = uView*vec4(hxz.x, y, hxz.y, 1.0);
  vp.y -= uCurv*(vp.z*vp.z);                               // rolling-world bend (view space)
  vDepth = -vp.z;
  vWXZ = hxz;
  gl_Position = uProj*vp;
  float col = mod(iType,uCols), row = floor(iType/uCols);
  vec2 luv = (aFace<0.5) ? (vec2(0.5)+aLocal*0.44) : vec2(0.5);   // top: hex uv · side: tile colour
  vUV = (vec2(col,row)+luv)*uCell;
  vLight = uAmbI + uDiffI*max(dot(normalize(aNormal),uLight),0.0);
  vWater = water*((aFace<0.5)?1.0:0.0);
}`

export const TILE_FS = `
precision highp float;
uniform sampler2D uTex; uniform vec3 uFog, uTint; uniform float uFogNear, uFogFar, uTime;
uniform float uCloud, uCloudScale; uniform vec2 uCloudOff;
varying vec2 vUV; varying float vDepth; varying float vLight; varying float vWater;
varying vec2 vWXZ;
void main(){
  vec4 c = texture2D(uTex, vUV);
  if(c.a < 0.5) discard;
  vec3 col = c.rgb * vLight * uTint;
  col += vWater * 0.06 * vec3(0.55,0.8,1.0) * (0.5 + 0.5*sin(uTime*2.3 + vUV.x*90.0 + vUV.y*70.0));
  if(uCloud > 0.003){                                       // drifting cloud-shadow noise
    vec2 cp = (vWXZ + uCloudOff) * uCloudScale;
    float cn = sin(cp.x + sin(cp.y*0.83+1.7)) * sin(cp.y*0.71 + sin(cp.x*0.67+4.2));
    col *= 1.0 - uCloud * 0.38 * smoothstep(0.05, 0.75, cn*0.5+0.5);
  }
  float f = smoothstep(uFogNear, uFogFar, vDepth);
  gl_FragColor = vec4(mix(col, uFog, f), 1.0);
}`

export const SKY_VS = `
precision highp float;
attribute vec2 aP; varying vec2 vP;
void main(){ vP = aP; gl_Position = vec4(aP, 0.9999, 1.0); }`

export const SKY_FS = `
precision highp float;
uniform vec3 uZen, uHor, uCamF, uCamR, uCamU, uSunDir, uSunCol, uMoonDir, uMoonCol;
uniform float uTanF, uAspect, uStar, uTime;
varying vec2 vP;
float shash(vec2 p){ return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453); }
void main(){
  vec3 ray = normalize(uCamF + uCamR*(vP.x*uTanF*uAspect) + uCamU*(vP.y*uTanF));
  vec3 col = mix(uHor, uZen, smoothstep(-0.08, 0.55, ray.y));
  float ds = max(dot(ray, uSunDir), 0.0);                   // sun disc + glow
  col += uSunCol * (smoothstep(0.9993, 0.9996, ds) + 0.25*pow(ds, 180.0));
  float dm = max(dot(ray, uMoonDir), 0.0);                  // moon disc
  col += uMoonCol * (smoothstep(0.9994, 0.9997, dm) + 0.08*pow(dm, 240.0));
  if(uStar > 0.003 && ray.y > 0.02){                        // hash stars, upper hemisphere
    vec2 sp = ray.xz / (ray.y + 0.35) * 14.0;
    vec2 cell = floor(sp);
    float hs = shash(cell);
    if(hs > 0.985){
      vec2 fp = fract(sp) - 0.5;
      float tw = 0.75 + 0.25*sin(uTime*(1.0+3.0*shash(cell+7.0)) + shash(cell+3.0)*6.28);
      col += uStar * tw * smoothstep(0.05, 0.0, dot(fp,fp)) * vec3(0.9,0.95,1.0);
    }
  }
  gl_FragColor = vec4(col, 1.0);
}`
