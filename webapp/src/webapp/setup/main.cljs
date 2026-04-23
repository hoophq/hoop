(ns webapp.setup.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text Spinner]]
   [webapp.components.forms :as forms]
   [webapp.config :as config]
   [reagent.core :as r]
   [re-frame.core :as rf]
   [webapp.setup.events]))

(defn- logo-dot []
  [:span {:style {:width "3px"
                  :height "3px"
                  :border-radius "50%"
                  :background "rgba(247,247,247,0.2)"
                  :display "inline-block"
                  :flex-shrink "0"}}])

(defn- left-panel []
  [:aside {:class "rounded-2xl flex flex-col justify-between relative overflow-hidden"
           :style {:background "linear-gradient(135deg, #0d1729 0%, #182449 35%, #1e2e5a 70%, #243570 100%)"
                   :padding "44px 40px"
                   :min-height "620px"}}
   [:div {:style {:position "absolute"
                  :top "-30%"
                  :right "-10%"
                  :width "70%"
                  :height "160%"
                  :background "radial-gradient(ellipse at center, rgba(176,176,176,0.08) 0%, transparent 70%)"
                  :pointer-events "none"}}]

   [:div {:style {:position "relative" :z-index 1}}
    [:figure {:class "flex items-center gap-2 mb-10"}
     [:img {:src (str config/webapp-url "/images/hoop-branding/SVG/hoop-symbol_white.svg")
            :class "w-5"
            :alt "hoop.dev"}]
     [:span {:class "text-sm font-bold tracking-tight" :style {:color "#F7F7F7"}} "hoop.dev"]]
    [:p {:class "text-xs font-semibold uppercase mb-5"
         :style {:letter-spacing "0.14em" :color "#B0B0B0"}}
     "Instance ready"]
    [:h1 {:class "text-2xl font-bold leading-tight mb-3"
          :style {:color "#F7F7F7" :letter-spacing "-0.025em" :max-width "340px"}}
     "The first account becomes the root admin."]
    [:p {:class "text-sm leading-relaxed"
         :style {:color "rgba(247,247,247,0.5)" :max-width "340px"}}
     "Once you sign up, the gateway starts intercepting connections. Everything below runs on your machine."]]

   [:div {:style {:margin "36px 0" :position "relative" :z-index 1}}
    [:p {:class "text-xs font-semibold uppercase mb-3"
         :style {:letter-spacing "0.14em" :color "rgba(247,247,247,0.35)"}}
     "Trusted in production by"]
    [:div {:class "flex flex-wrap items-center gap-2 pb-5 mb-5"
           :style {:border-bottom "1px solid rgba(247,247,247,0.08)"}}
     [:span {:class "text-sm font-bold" :style {:color "rgba(247,247,247,0.55)"}} "EBANX"]
     [logo-dot]
     [:span {:class "text-sm font-bold" :style {:color "rgba(247,247,247,0.55)"}} "RD Station"]
     [logo-dot]
     [:span {:class "text-sm font-bold" :style {:color "rgba(247,247,247,0.55)"}} "Dock"]
     [logo-dot]
     [:span {:class "text-sm font-bold" :style {:color "rgba(247,247,247,0.55)"}} "PicPay"]
     [logo-dot]
     [:span {:class "text-sm font-bold" :style {:color "rgba(247,247,247,0.55)"}} "Unico"]]
    [:figure {:style {:margin 0 :padding-left "16px" :border-left "2px solid #B0B0B0"}}
     [:blockquote {:class "text-sm leading-relaxed m-0"
                   :style {:color "rgba(247,247,247,0.82)"}}
      "Zero setup for GDPR, SOC2, and PCI across our databases, Kubernetes clusters, and AWS accounts. We replaced our in-house tool in a week."]
     [:figcaption {:class "mt-3 flex items-center gap-2 text-xs"
                   :style {:color "rgba(247,247,247,0.45)"}}
      [:span {:class "font-semibold" :style {:color "#F7F7F7"}} "Staff Engineer"]
      [:span "·"]
      [:span {:class "font-medium"} "RD Station"]]]]

   [:div {:class "flex items-center gap-2 pt-5" :style {:position "relative" :z-index 1}}
    [:span {:class "font-mono text-xs" :style {:color "rgba(247,247,247,0.35)"}} "localhost:8009"]
    [:div {:style {:flex 1 :height "1px" :background "rgba(247,247,247,0.08)"}}]
    [:div {:class "flex items-center gap-1.5"}
     [:div {:class "w-1.5 h-1.5 rounded-full animate-pulse" :style {:background "#B0B0B0"}}]
     [:span {:class "text-xs font-semibold uppercase tracking-wider"
             :style {:color "rgba(247,247,247,0.5)"}}
      "Gateway online"]]]])

(defn- password-strength-score [pw]
  (let [score (cond-> 0
                (>= (count pw) 8) inc
                (>= (count pw) 12) inc
                (and (re-find #"[A-Z]" pw) (re-find #"[a-z]" pw)) inc
                (and (re-find #"\d" pw) (re-find #"[^\w\s]" pw)) inc)]
    (min score 4)))

(defn- strength-label [score]
  (case score 1 "weak" 2 "fair" 3 "good" 4 "strong" ""))

(defn- form-panel []
  (let [fullname (r/atom "")
        email (r/atom "")
        password (r/atom "")
        pw-score (r/atom 0)
        loading (rf/subscribe [:setup->loading])
        error (rf/subscribe [:setup->error])]
    (fn []
      [:> Box {:class "bg-white rounded-2xl flex flex-col justify-center"
               :style {:padding "48px 44px" :border "1px solid var(--gray-4)"}}

       [:> Flex {:align "center" :gap "3" :mb "7"}
        [:> Text {:size "1" :weight "medium" :class "uppercase tracking-wider text-gray-10"}
         "Create admin"]]

       [:> Heading {:as "h2" :size "7" :weight "bold" :class "text-gray-12 mb-1"}
        "Set up your instance"]
       [:> Text {:as "p" :size "2" :class "text-gray-11 mb-5"}
        "Takes less than a minute. Add a connection right after."]

       [:form {:on-submit (fn [e]
                            (.preventDefault e)
                            (when-not @loading
                              (rf/dispatch [:setup->create-admin
                                            {:name @fullname
                                             :email @email
                                             :password @password}])))}

        [forms/input {:label "Full name"
                      :placeholder "Jane Cooper"
                      :value @fullname
                      :required true
                      :type "text"
                      :on-change #(reset! fullname (-> % .-target .-value))}]

        [forms/input {:label "Work email"
                      :placeholder "jane@company.com"
                      :value @email
                      :required true
                      :type "email"
                      :on-change #(reset! email (-> % .-target .-value))}]

        [:> Box {:class "mb-regular"}
         [:> Flex {:justify "between" :align "baseline" :mb "1"}
          [:> Text {:size "1" :as "label" :weight "bold" :class "text-gray-12"} "Password"]
          (when (pos? @pw-score)
            [:> Text {:size "1" :class "font-mono text-gray-11"} (strength-label @pw-score)])]
         [forms/input {:placeholder "At least 12 characters"
                       :value @password
                       :required true
                       :type "password"
                       :minlength 12
                       :not-margin-bottom? true
                       :on-change (fn [e]
                                    (let [v (-> e .-target .-value)]
                                      (reset! password v)
                                      (reset! pw-score (password-strength-score v))))}]
         [:> Flex {:gap "1" :mt "2"}
          (for [i (range 4)]
            ^{:key i}
            [:div {:class "flex-1 rounded-sm h-0.5 transition-all"
                   :style {:background (if (< i @pw-score) "var(--gray-12)" "var(--gray-4)")}}])]]

        (when @error
          [:> Text {:as "p" :size "1" :color "red" :mb "2"}
           (or (get-in @error [:body :message]) "Something went wrong. Please try again.")])

        [:> Button {:type "submit"
                    :size "3"
                    :class "w-full mt-5"
                    :disabled @loading}
         (if @loading
           [:<> [:> Spinner {:size "1"}] "Creating account..."]
           "Create admin account")]

        [:> Flex {:align "center" :justify "center" :gap "2" :mt "4"}
         [:svg {:width "11" :height "11" :viewBox "0 0 24 24" :fill "none"
                :stroke "currentColor" :stroke-width "2"
                :class "text-gray-10 flex-shrink-0"}
          [:rect {:x "3" :y "11" :width "18" :height "11" :rx "2"}]
          [:path {:d "M7 11V7a5 5 0 0 1 10 0v4"}]]
         [:> Text {:size "1" :class "text-gray-10"}
          "Runs locally. Credentials never leave this machine."]]]])))

(defn main []
  [:div {:class "min-h-screen bg-gray-100 flex items-center justify-center p-8"}
   [:div {:class "w-full grid gap-6"
          :style {:max-width "1080px"
                  :grid-template-columns "1fr 1fr"}}
    [left-panel]
    [form-panel]]])
