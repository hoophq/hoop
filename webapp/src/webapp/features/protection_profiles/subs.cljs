(ns webapp.features.protection-profiles.subs
  (:require [re-frame.core :as rf]))

;; Display titles matching webapp_v2/src/features/ProtectionProfiles/constants.js
(def ^:private profile-display-names
  {"hipaa-ready" "HIPAA Ready"
   "soc2-type2" "SOC2 Type II"
   "protection-permissive" "Essential Guardrails"
   "protection-medium" "Balanced"
   "protection-high" "Maximum"})

(rf/reg-sub
 :protection-profile/active
 (fn [db _]
   (get-in db [:protection-profile :active])))

;; The read-only pill shown in the role Attributes field while a protection
;; profile is active. nil when the org is on manual configuration or the
;; fetch failed.
(rf/reg-sub
 :protection-profile/managed-pill
 :<- [:protection-profile/active]
 (fn [active _]
   (let [{:keys [profile attribute-name]} active]
     (when (and profile attribute-name)
       {:attribute-name attribute-name
        :display-name (get profile-display-names profile attribute-name)}))))
