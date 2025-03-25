(ns webapp.upgrade-plan.main
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Button Flex Heading Text]]
   ["lucide-react" :refer [ListChecks MessagesSquare Sparkles]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.button :as button]
   [webapp.config :as config]))

(defn- feature [{:keys [icon title description]}]
  [:> Flex {:align "center" :gap "4"}
   [:> Avatar {:fallback (r/as-element icon)
               :size "4"}]
   [:> Box
    [:> Heading {:as "h3" :size "5" :weight "bold" :class "text-[--gray-12]"}
     title]
    [:> Text {:size "3" :class "text-[--gray-12]"}
     description]]])

(defn main [remove-back?]
  [:> Box {:class "bg-white relative overflow-hidden"}
   (when-not remove-back?
     [:> Box {:p "5"}
      [button/HeaderBack]])

   [:> Flex {:align "center" :justify "between" :gap "8" :p "9"}
    [:> Box {:class "w-2/3 xl:w-1/2 space-y-12 pr-0 2xl:pr-16"}
     [:> Box {:class "space-y-4"}
      [:> Heading {:as "h1" :size "8" :weight "bold" :class "text-[--gray-12]"}
       "Get more for your connections"]
      [:> Text {:size "5" :class "text-[--gray-11]"}
       "Upgrade to Enterprise plan and boost your experience."]]

     [:> Box {:class "space-y-8"}
      [feature {:icon [:> Sparkles {:size 20}]
                :title "AI-Enhanced developer experience"
                :description "Power up development with AI-driven query suggestions and automated data masking while maintaining security standards."}]

      [feature {:icon [:> ListChecks {:size 20}]
                :title "Complete visibility & control"
                :description "Monitor database and infrastructure interaction with detailed session recordings and instant alerts in your favorite tools."}]

      [feature {:icon [:> MessagesSquare {:size 20}]
                :title "Enterprise-grade support"
                :description "Access priority support through Slack, Teams, or email, plus dedicated onboarding to accelerate your team experience."}]]

     [:> Button {:size "4"
                 :on-click (fn []
                             (rf/dispatch [:modal->close])
                             (js/window.Intercom
                              "showNewMessage"
                              "I want to upgrade my current plan"))}
      "Request a demo"]]

    [:> Box {:class (str "mt-[--space-9] absolute top-1/2 -translate-y-1/2 right-0 w-1/2 h-auto "
                         "transform translate-x-1/4 xl:translate-x-16 2xl:translate-x-10")}
     [:> Box {:class "h-full w-full relative"}
      [:img {:src (str config/webapp-url "/images/upgrade-plan.png")
             :alt "Terminal interface"
             :class (str "w-full h-[578px] "
                         "object-cover object-left")}]]]]])
