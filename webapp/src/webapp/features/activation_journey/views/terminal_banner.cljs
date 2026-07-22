(ns webapp.features.activation-journey.views.terminal-banner
  (:require
   ["@radix-ui/themes" :refer [Box]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.features.activation-journey.views.enterprise-banner :as enterprise-banner]
   [webapp.features.activation-journey.views.feature-cards :as feature-cards]
   [webapp.features.activation-journey.views.features-modal :as features-modal]))

(defn- connection-subtype
  "Mirrors webapp.webclient.panel/discover-connection-type."
  [connection]
  (cond
    (not (cs/blank? (:subtype connection))) (:subtype connection)
    (not (cs/blank? (:icon_name connection))) (:icon_name connection)
    :else (:type connection)))

(defn- modal-props [connection subtype]
  {:subtype subtype
   :connection-ids (when (:id connection) [(:id connection)])
   :connection-names (when (:name connection) [(:name connection)])})

(defn- dismiss! []
  (rf/dispatch [:activation-journey/dismiss-terminal-banner]))

(defn main
  "Activation-journey banner pinned to the top of the terminal's logs panel.
  The banner model (or nil) comes from :activation-journey/terminal-banner —
  free plan sees a persistent unlock banner that starts cycling feature
  templates after the first execution; enterprise only sees the (dismissible)
  cycling banner after a run."
  []
  (let [connection @(rf/subscribe [:primary-connection/selected])
        subtype (connection-subtype connection)
        admin? @(rf/subscribe [:activation-journey/admin?])
        requested? @(rf/subscribe [:activation-journey/requested?])
        props (modal-props connection subtype)
        banner @(rf/subscribe [:activation-journey/terminal-banner
                               {:subtype subtype
                                :connection-ids (:connection-ids props)
                                :connection-names (:connection-names props)}])]
    ;; The terminal can mount before the current user finishes loading, in
    ;; which case the panel-level init was a no-op; request once when the
    ;; admin flag settles.
    (when (and admin? (not requested?))
      (rf/dispatch [:activation-journey/init {}]))
    (when banner
      [:> Box {:class "px-2 pt-2"}
       (case (:variant banner)
         :unlock
         [enterprise-banner/main
          {:primary {:label "See features"
                     :on-click #(features-modal/open! props)}}]

         :protect
         [enterprise-banner/main
          {:title "Protect your resource with our features"
           :primary {:label "See features"
                     :on-click #(features-modal/open! props)}
           :secondary {:label "Not now" :on-click dismiss!}}]

         :template
         [enterprise-banner/main
          {:title (:title banner)
           :badge-label (:label banner)
           :subtitle (:description banner)
           :primary {:label (:banner-cta banner)
                     :on-click #(feature-cards/dispatch-cta!
                                 {:feature (:feature banner)
                                  :cta {:kind :create-with-template
                                        :label (:banner-cta banner)
                                        :template-id (:template-id banner)}}
                                 {:surface :terminal
                                  :connection-ids (:connection-ids props)
                                  :connection-names (:connection-names props)})}
           :secondary (case (:secondary banner)
                        :see-all {:label "See all Features"
                                  :on-click #(features-modal/open! props)}
                        :not-now {:label "Not now" :on-click dismiss!}
                        nil)}]

         nil)])))
