(ns webapp.setup.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Text Spinner]]
   [webapp.components.forms :as forms]
   [webapp.config :as config]
   [reagent.core :as r]
   [re-frame.core :as rf]
   [webapp.setup.events]))

(defn- logo-dot []
  [:> Box {:class "w-[3px] h-[3px] rounded-full inline-block shrink-0 bg-white/20"}])

(defn- left-panel []
  [:> Box {:as "aside"
           :class "rounded-2xl flex flex-col justify-between relative overflow-hidden py-11 px-10 min-h-[620px]"
           :style {:background "linear-gradient(135deg, #0d1729 0%, #182449 35%, #1e2e5a 70%, #243570 100%)"}}
   [:> Box {:class "absolute -top-[30%] -right-[10%] w-[70%] h-[160%] pointer-events-none"
            :style {:background "radial-gradient(ellipse at center, rgba(176,176,176,0.08) 0%, transparent 70%)"}}]

   [:> Box {:class "relative z-10"}
    [:figure {:class "flex items-center gap-2 mb-10"}
     [:img {:src (str config/webapp-url "/images/hoop-branding/SVG/hoop-symbol_white.svg")
            :class "w-5"
            :alt "hoop.dev"}]
     [:> Text {:size "2" :weight "bold" :class "tracking-tight text-white"} "hoop.dev"]]
    [:> Text {:as "p" :size "1" :weight "bold" :class "uppercase mb-5 tracking-[0.14em] text-white/70"}
     "Instance ready"]
    [:> Heading {:as "h1" :size "5" :weight "bold" :class "leading-tight mb-3 tracking-tight text-white max-w-[340px]"}
     "The first account becomes the root admin."]
    [:> Text {:as "p" :size "2" :class "leading-relaxed text-white/50 max-w-[340px]"}
     "Once you sign up, the gateway starts intercepting connections. Everything below runs on your machine."]]

   [:> Box {:class "my-9 relative z-10"}
    [:> Text {:as "p" :size "1" :weight "bold" :class "uppercase mb-3 tracking-[0.14em] text-white/35"}
     "Trusted in production by"]
    [:> Flex {:wrap "wrap" :align "center" :gap "2" :class "pb-5 mb-5 border-b border-white/[0.08]"}
     [:> Text {:size "2" :weight "bold" :class "text-white/55"} "EBANX"]
     [logo-dot]
     [:> Text {:size "2" :weight "bold" :class "text-white/55"} "RD Station"]
     [logo-dot]
     [:> Text {:size "2" :weight "bold" :class "text-white/55"} "Dock"]
     [logo-dot]
     [:> Text {:size "2" :weight "bold" :class "text-white/55"} "PicPay"]
     [logo-dot]
     [:> Text {:size "2" :weight "bold" :class "text-white/55"} "Unico"]]
    [:figure {:class "m-0 pl-4 border-l-2 border-white/70"}
     [:blockquote {:class "text-sm leading-relaxed m-0 text-white/80"}
      "Zero setup for GDPR, SOC2, and PCI across our databases, Kubernetes clusters, and AWS accounts. We replaced our in-house tool in a week."]
     [:figcaption {:class "mt-3 flex items-center gap-2 text-xs text-white/45"}
      [:> Text {:as "span" :size "1" :weight "bold" :class "text-white"} "Staff Engineer"]
      [:> Text {:as "span" :size "1"} "·"]
      [:> Text {:as "span" :size "1" :weight "medium"} "RD Station"]]]]

   [:> Flex {:align "center" :gap "2" :class "pt-5 relative z-10"}
    [:> Text {:size "1" :class "font-mono text-white/35"} "localhost:8009"]
    [:> Box {:class "flex-1 h-px bg-white/[0.08]"}]
    [:> Flex {:align "center" :class "gap-1.5"}
     [:> Box {:class "w-1.5 h-1.5 rounded-full animate-pulse bg-white/70"}]
     [:> Text {:size "1" :weight "bold" :class "uppercase tracking-wider text-white/50"}
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
      [:> Box {:class "bg-white rounded-2xl flex flex-col justify-center px-11 py-12 border border-gray-4"}

       [:> Flex {:align "center" :gap "3" :mb "7"}
        [:> Text {:size "1" :weight "medium" :class "uppercase tracking-wider text-gray-10"}
         "Create admin"]]

       [:> Heading {:as "h2" :size "7" :weight "bold" :mb "1"}
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
          [:> Text {:size "1" :as "label" :weight "bold"} "Password"]
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
            [:> Box {:class (str "flex-1 rounded-sm h-0.5 transition-all "
                                 (if (< i @pw-score) "bg-gray-12" "bg-gray-4"))}])]]

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
  [:> Box {:class "min-h-screen bg-gray-2 flex items-center justify-center p-8"}
   [:> Grid {:columns "2" :gap "6" :class "w-full max-w-[1080px]"}
    [left-panel]
    [form-panel]]])
